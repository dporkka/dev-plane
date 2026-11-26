package prfactory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

// ErrGiteaRepositoryBinding is a fail-closed authority failure. A local
// connection, a shared service token, or a signed webhook is not tenant proof.
var ErrGiteaRepositoryBinding = errors.New("gitea repository binding is not verified")

// GiteaBindingRequest is an explicit operator grant. All four identifiers
// must be obtained independently of the tenant-submitted connection name.
type GiteaBindingRequest struct {
	RepositoryID         string
	ProjectID            string
	OrganizationID       string
	UpstreamRepositoryID int64
}

// GiteaBinding is the verified local-to-external identity. No secrets belong
// in this value or in the durable binding table.
type GiteaBinding struct {
	RepositoryID         string
	ProjectID            string
	OrganizationID       string
	InstanceURL          string
	UpstreamRepositoryID int64
	Owner                string
	Name                 string
	FullName             string
	CloneURL             string
}

type giteaBindingRecord struct {
	identity                                                  GiteaBinding
	settings                                                  string
	boundProject, boundOrganization, boundInstance, boundName sql.NullString
	boundUpstream                                             sql.NullInt64
}

type bindingQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ConfiguredGiteaBindingForge loads only the operator-selected instance and
// API credential. Neither a request URL nor a Git push password may select it.
func ConfiguredGiteaBindingForge() (*gateway.GiteaForge, gateway.ForgeCredential, error) {
	root, err := gateway.GiteaWebhookInstanceURL(os.Getenv("GITEA_URL"))
	token := os.Getenv("GITEA_TOKEN")
	if err != nil || !validBindingToken(token) {
		return nil, gateway.ForgeCredential{}, fmt.Errorf("%w: configured Gitea instance and API credential required", ErrGiteaRepositoryBinding)
	}
	forge, err := gateway.NewGiteaForge(root, nil)
	if err != nil {
		return nil, gateway.ForgeCredential{}, ErrGiteaRepositoryBinding
	}
	return forge, gateway.ForgeCredential{AccessToken: token}, nil
}

func validBindingToken(token string) bool {
	return token != "" && strings.TrimSpace(token) == token && !strings.ContainsAny(token, "\r\n\x00")
}

func validBindingSegment(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 255 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func loadGiteaBinding(ctx context.Context, q bindingQueryer, repositoryID, projectID string) (giteaBindingRecord, error) {
	var out giteaBindingRecord
	out.identity.RepositoryID = repositoryID
	out.identity.ProjectID = projectID
	var settings sql.NullString
	err := q.QueryRowContext(ctx, `
        SELECT r.owner,r.name,r.full_name,r.clone_url,r.settings,p.organization_id,
               b.project_id,b.organization_id,b.instance_url,b.upstream_repository_id,b.full_name
        FROM repositories r
        JOIN projects p ON p.id=r.project_id
        JOIN organizations o ON o.id=p.organization_id
        LEFT JOIN gitea_repository_bindings b ON b.repository_id=r.id
        WHERE r.id=$1 AND r.project_id=$2 AND r.deleted_at IS NULL
          AND p.deleted_at IS NULL AND o.deleted_at IS NULL
    `, repositoryID, projectID).Scan(
		&out.identity.Owner, &out.identity.Name, &out.identity.FullName, &out.identity.CloneURL, &settings, &out.identity.OrganizationID,
		&out.boundProject, &out.boundOrganization, &out.boundInstance, &out.boundUpstream, &out.boundName)
	if err != nil {
		return giteaBindingRecord{}, fmt.Errorf("%w: active repository/project or binding schema unavailable", ErrGiteaRepositoryBinding)
	}
	if !settings.Valid {
		return giteaBindingRecord{}, ErrGiteaRepositoryBinding
	}
	out.settings = settings.String
	authority, err := models.ParseRepositoryForgeSettings([]byte(out.settings))
	if err != nil || authority.Provider != models.ForgeProviderGitea {
		return giteaBindingRecord{}, ErrGiteaRepositoryBinding
	}
	root, err := gateway.GiteaWebhookInstanceURL(authority.BaseURL)
	if err != nil || !validBindingSegment(out.identity.Owner) || !validBindingSegment(out.identity.Name) ||
		out.identity.FullName != out.identity.Owner+"/"+out.identity.Name ||
		out.identity.CloneURL != root+"/"+out.identity.FullName+".git" || out.identity.OrganizationID == "" {
		return giteaBindingRecord{}, fmt.Errorf("%w: local repository identity is invalid", ErrGiteaRepositoryBinding)
	}
	out.identity.InstanceURL = root
	return out, nil
}

func (r giteaBindingRecord) matches(expectedID int64) bool {
	return expectedID > 0 && r.boundUpstream.Valid && r.boundUpstream.Int64 == expectedID &&
		r.boundProject.Valid && r.boundProject.String == r.identity.ProjectID &&
		r.boundOrganization.Valid && r.boundOrganization.String == r.identity.OrganizationID &&
		r.boundInstance.Valid && r.boundInstance.String == r.identity.InstanceURL &&
		r.boundName.Valid && r.boundName.String == r.identity.FullName
}

func verifyGiteaUpstream(ctx context.Context, forge gateway.Forge, credential gateway.ForgeCredential, identity GiteaBinding) (*gateway.ForgeRepository, error) {
	if forge == nil || forge.Name() != "gitea" || !validBindingToken(credential.AccessToken) || identity.UpstreamRepositoryID <= 0 {
		return nil, ErrGiteaRepositoryBinding
	}
	authorityProvider, ok := forge.(gateway.ForgeAuthorityProvider)
	if !ok {
		return nil, ErrGiteaRepositoryBinding
	}
	authority := authorityProvider.Authority()
	root, err := gateway.GiteaWebhookInstanceURL(authority.BaseURL)
	if err != nil || authority.Provider != "gitea" || root != identity.InstanceURL {
		return nil, fmt.Errorf("%w: configured instance does not match binding", ErrGiteaRepositoryBinding)
	}
	// Read from the exact operator-controlled instance, before any mutation.
	upstream, err := forge.GetRepository(ctx, credential, identity.Owner, identity.Name)
	if err != nil || upstream == nil {
		return nil, fmt.Errorf("%w: upstream lookup failed", ErrGiteaRepositoryBinding)
	}
	if upstream.ID != identity.UpstreamRepositoryID || upstream.Name != identity.Name ||
		upstream.FullName != identity.FullName || upstream.HTMLURL != root+"/"+identity.FullName ||
		upstream.CloneURL != identity.CloneURL || strings.TrimSpace(upstream.DefaultBranch) == "" {
		return nil, fmt.Errorf("%w: upstream identity changed", ErrGiteaRepositoryBinding)
	}
	return upstream, nil
}

// BindGiteaRepository persists an operator-approved, independently verified
// grant. This is an administrative operation, not an HTTP tenant endpoint.
// An exact repeat is idempotent; a different grant cannot overwrite it.
func BindGiteaRepository(ctx context.Context, db *sql.DB, forge gateway.Forge, credential gateway.ForgeCredential, req GiteaBindingRequest) (GiteaBinding, error) {
	if db == nil || req.RepositoryID == "" || req.ProjectID == "" || req.OrganizationID == "" || req.UpstreamRepositoryID <= 0 {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	before, err := loadGiteaBinding(ctx, db, req.RepositoryID, req.ProjectID)
	if err != nil {
		return GiteaBinding{}, err
	}
	if before.identity.OrganizationID != req.OrganizationID || (before.boundUpstream.Valid && !before.matches(req.UpstreamRepositoryID)) {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	identity := before.identity
	identity.UpstreamRepositoryID = req.UpstreamRepositoryID
	upstream, err := verifyGiteaUpstream(ctx, forge, credential, identity)
	if err != nil {
		return GiteaBinding{}, err
	}

	// Do not hold a database transaction across the network. Re-read the
	// complete local authorization after the provider lookup, then reserve
	// the external ID atomically. Conflicts require an explicit retry/review.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return GiteaBinding{}, fmt.Errorf("%w: start binding transaction", ErrGiteaRepositoryBinding)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadGiteaBinding(ctx, tx, req.RepositoryID, req.ProjectID)
	if err != nil || current != before {
		return GiteaBinding{}, fmt.Errorf("%w: local identity changed during verification", ErrGiteaRepositoryBinding)
	}
	if !current.boundUpstream.Valid {
		result, err := tx.ExecContext(ctx, `
            INSERT INTO gitea_repository_bindings
                (repository_id,project_id,organization_id,instance_url,upstream_repository_id,full_name)
            VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING
        `, identity.RepositoryID, identity.ProjectID, identity.OrganizationID, identity.InstanceURL, identity.UpstreamRepositoryID, identity.FullName)
		if err != nil {
			return GiteaBinding{}, fmt.Errorf("%w: reserve upstream identity", ErrGiteaRepositoryBinding)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return GiteaBinding{}, fmt.Errorf("%w: upstream identity is already reserved", ErrGiteaRepositoryBinding)
		}
	}
	result, err := tx.ExecContext(ctx, `
        UPDATE repositories SET connection_status='connected',default_branch=$1,private=$2,
            last_synced_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP
        WHERE id=$3 AND project_id=$4 AND deleted_at IS NULL
    `, upstream.DefaultBranch, upstream.Private, identity.RepositoryID, identity.ProjectID)
	if err != nil {
		return GiteaBinding{}, fmt.Errorf("%w: update verified connection", ErrGiteaRepositoryBinding)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	if err := tx.Commit(); err != nil {
		return GiteaBinding{}, fmt.Errorf("%w: commit binding transaction", ErrGiteaRepositoryBinding)
	}
	return identity, nil
}

// VerifyGiteaRepositoryBinding checks current project/tenant ownership,
// immutable binding, and fresh upstream state. A name-only connection must
// fail locally, without touching the provider.
func VerifyGiteaRepositoryBinding(ctx context.Context, db *sql.DB, forge gateway.Forge, credential gateway.ForgeCredential, repositoryID, projectID string) (GiteaBinding, error) {
	if db == nil || repositoryID == "" || projectID == "" {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	record, err := loadGiteaBinding(ctx, db, repositoryID, projectID)
	if err != nil {
		return GiteaBinding{}, err
	}
	if !record.matches(record.boundUpstream.Int64) {
		return GiteaBinding{}, ErrGiteaRepositoryBinding
	}
	identity := record.identity
	identity.UpstreamRepositoryID = record.boundUpstream.Int64
	if _, err := verifyGiteaUpstream(ctx, forge, credential, identity); err != nil {
		return GiteaBinding{}, err
	}
	// Detect local changes while the remote request was in flight.
	current, err := loadGiteaBinding(ctx, db, repositoryID, projectID)
	if err != nil || current != record {
		return GiteaBinding{}, fmt.Errorf("%w: local binding changed", ErrGiteaRepositoryBinding)
	}
	return identity, nil
}

func (f *Factory) verifyGiteaTarget(ctx context.Context, repositoryID, projectID string, target repositoryTarget) error {
	if target.Forge.Provider != models.ForgeProviderGitea {
		return nil
	}
	binding, err := VerifyGiteaRepositoryBinding(ctx, f.db, f.forge, f.forgeCredential, repositoryID, projectID)
	if err != nil {
		return err
	}
	if binding.Owner != target.Owner || binding.Name != target.Name {
		return ErrGiteaRepositoryBinding
	}
	return nil
}

// GiteaCredentialsConfigured identifies a credential-bearing worker.
func GiteaCredentialsConfigured() bool {
	for _, key := range []string{"GITEA_TOKEN", "GITEA_CLONE_TOKEN", "GITEA_GIT_PASSWORD", "GITEA_WEBHOOK_SECRET"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}
