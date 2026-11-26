package prfactory

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"unicode"

	"github.com/ai-dev-control-plane/gateway"
)

// GiteaBindingLifecycleRequest identifies an immutable reservation and the
// expected generation. Reason is non-secret operator-supplied audit context.
type GiteaBindingLifecycleRequest struct {
	GiteaBindingRequest
	InstanceURL        string
	ExpectedGeneration int64
	Reason             string
}

func (r GiteaBindingLifecycleRequest) valid() bool {
	root, err := gateway.GiteaWebhookInstanceURL(r.InstanceURL)
	return err == nil && root == r.InstanceURL && r.RepositoryID != "" &&
		r.ProjectID != "" && r.OrganizationID != "" && r.UpstreamRepositoryID > 0 &&
		r.ExpectedGeneration > 0 && r.ExpectedGeneration < math.MaxInt64 &&
		len(r.Reason) > 0 && len(r.Reason) <= 256 && strings.TrimSpace(r.Reason) == r.Reason &&
		strings.IndexFunc(r.Reason, unicode.IsControl) == -1
}

func recordBindingLifecycle(ctx context.Context, tx *sql.Tx, r GiteaBindingLifecycleRequest, action string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO gitea_binding_lifecycle_events (repository_id,authorization_generation,action,reason)
		VALUES ($1,$2,$3,$4)
	`, r.RepositoryID, r.ExpectedGeneration+1, action, r.Reason)
	if err != nil {
		return ErrGiteaRepositoryBinding
	}
	return nil
}

func changedOne(result sql.Result, err error) bool {
	if err != nil {
		return false
	}
	rows, err := result.RowsAffected()
	return err == nil && rows == 1
}

// RevokeGiteaRepositoryBinding needs only the local DB and exact operator
// identifiers. The reservation is retained even after tenant soft deletion.
func RevokeGiteaRepositoryBinding(ctx context.Context, db *sql.DB, r GiteaBindingLifecycleRequest) (int64, error) {
	if db == nil || !r.valid() {
		return 0, ErrGiteaRepositoryBinding
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, ErrGiteaRepositoryBinding
	}
	defer func() { _ = tx.Rollback() }()
	if !changedOne(tx.ExecContext(ctx, `
		UPDATE gitea_repository_bindings SET authorization_generation=$1,revoked_at=CURRENT_TIMESTAMP
		WHERE repository_id=$2 AND project_id=$3 AND organization_id=$4 AND instance_url=$5
		AND upstream_repository_id=$6 AND authorization_generation=$7 AND revoked_at IS NULL
	`, r.ExpectedGeneration+1, r.RepositoryID, r.ProjectID, r.OrganizationID, r.InstanceURL, r.UpstreamRepositoryID, r.ExpectedGeneration)) {
		return 0, ErrGiteaRepositoryBinding
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE repositories SET connection_status='disconnected',last_synced_at=NULL,updated_at=CURRENT_TIMESTAMP
		WHERE id=$1 AND project_id=$2 AND deleted_at IS NULL
	`, r.RepositoryID, r.ProjectID); err != nil {
		return 0, ErrGiteaRepositoryBinding
	}
	if err := recordBindingLifecycle(ctx, tx, r, "revoke"); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, ErrGiteaRepositoryBinding
	}
	return r.ExpectedGeneration + 1, nil
}

// ReactivateGiteaRepositoryBinding verifies the same reservation with freshly
// configured credentials. It cannot prove that upstream tokens were rotated;
// that remains an explicit operator obligation.
func ReactivateGiteaRepositoryBinding(ctx context.Context, db *sql.DB, forge gateway.Forge, credential gateway.ForgeCredential, r GiteaBindingLifecycleRequest) (GiteaBinding, error) {
	if db == nil || !r.valid() {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	generation, err := ConfiguredGiteaBindingGeneration()
	if err != nil || generation != r.ExpectedGeneration+1 {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	before, err := loadGiteaBinding(ctx, db, r.RepositoryID, r.ProjectID)
	if err != nil || !before.matchesIdentity(r.UpstreamRepositoryID) || !before.revokedAt.Valid ||
		before.generation.Int64 != r.ExpectedGeneration || before.identity.InstanceURL != r.InstanceURL ||
		before.identity.OrganizationID != r.OrganizationID {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	identity := before.identity
	identity.UpstreamRepositoryID = r.UpstreamRepositoryID
	identity.AuthorizationGeneration = generation
	upstream, err := verifyGiteaUpstream(ctx, forge, credential, identity)
	if err != nil {
		return GiteaBinding{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadGiteaBinding(ctx, tx, r.RepositoryID, r.ProjectID)
	if err != nil || current != before {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	if !changedOne(tx.ExecContext(ctx, `
		UPDATE gitea_repository_bindings SET authorization_generation=$1,revoked_at=NULL,verified_at=CURRENT_TIMESTAMP
		WHERE repository_id=$2 AND authorization_generation=$3 AND revoked_at IS NOT NULL
	`, generation, r.RepositoryID, r.ExpectedGeneration)) {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	if !changedOne(tx.ExecContext(ctx, `
		UPDATE repositories SET connection_status='connected',default_branch=$1,private=$2,
		last_synced_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP
		WHERE id=$3 AND project_id=$4 AND deleted_at IS NULL
	`, upstream.DefaultBranch, upstream.Private, r.RepositoryID, r.ProjectID)) {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	if err := recordBindingLifecycle(ctx, tx, r, "reactivate"); err != nil {
		return GiteaBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	return identity, nil
}

// CheckGiteaRepositoryBindingGeneration is a local fail-closed authority check
// for an already verified generation; it never touches the provider.
func CheckGiteaRepositoryBindingGeneration(ctx context.Context, db *sql.DB, repositoryID, projectID string, generation int64) error {
	configured, err := ConfiguredGiteaBindingGeneration()
	if err != nil || configured != generation || db == nil {
		return ErrGiteaRepositoryBinding
	}
	record, err := loadGiteaBinding(ctx, db, repositoryID, projectID)
	if err != nil || !record.matches(record.boundUpstream.Int64) || record.generation.Int64 != generation {
		return ErrGiteaRepositoryBinding
	}
	return nil
}
