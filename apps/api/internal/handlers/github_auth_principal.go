package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/models"
)

// githubOAuthProfile comes from GitHub's authenticated /user endpoint. The
// numeric provider ID, never the mutable login, is the identity key.
type githubOAuthProfile struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type githubOAuthPrincipal struct {
	UserID string
	OrgID  string
	Role   string
}

// resolveGitHubOAuthPrincipal obtains organization/role from a persisted,
// active user, or atomically provisions a private first-user organization.
// Never infer permissions from a GitHub login, email address, or OAuth token.
func resolveGitHubOAuthPrincipal(ctx context.Context, db *sql.DB, profile githubOAuthProfile) (githubOAuthPrincipal, error) {
	if profile.ID <= 0 || strings.TrimSpace(profile.Email) == "" {
		return githubOAuthPrincipal{}, errors.New("missing GitHub subject or email")
	}
	providerID := strconv.FormatInt(profile.ID, 10)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return githubOAuthPrincipal{}, fmt.Errorf("start GitHub identity transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// LIMIT 2 deliberately fails closed on duplicate provider identity rows.
	// Include soft-deleted accounts so deprovisioned users are never
	// automatically recreated under a fresh tenant.
	rows, err := tx.QueryContext(ctx, `
        SELECT u.id, u.organization_id, u.role, u.deleted_at, o.deleted_at
        FROM users u
        JOIN organizations o ON o.id = u.organization_id
        WHERE u.github_id = $1
        LIMIT 2
    `, providerID)
	if err != nil {
		return githubOAuthPrincipal{}, fmt.Errorf("load GitHub identity: %w", err)
	}
	var principal githubOAuthPrincipal
	var userDeleted, orgDeleted sql.NullTime
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&principal.UserID, &principal.OrgID, &principal.Role, &userDeleted, &orgDeleted); err != nil {
			_ = rows.Close()
			return githubOAuthPrincipal{}, fmt.Errorf("scan GitHub identity: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return githubOAuthPrincipal{}, fmt.Errorf("iterate GitHub identities: %w", err)
	}
	if err := rows.Close(); err != nil {
		return githubOAuthPrincipal{}, fmt.Errorf("close GitHub identities: %w", err)
	}

	if count > 1 {
		return githubOAuthPrincipal{}, errors.New("ambiguous GitHub identity")
	}
	if count == 1 {
		if userDeleted.Valid || orgDeleted.Valid {
			return githubOAuthPrincipal{}, errors.New("GitHub identity is disabled")
		}
		if principal.UserID == "" || principal.OrgID == "" {
			return githubOAuthPrincipal{}, errors.New("incomplete GitHub membership")
		}
		switch principal.Role {
		case models.RoleOwner, models.RoleAdmin, models.RoleMember:
		default:
			return githubOAuthPrincipal{}, errors.New("unrecognized GitHub membership role")
		}
		if err := tx.Commit(); err != nil {
			return githubOAuthPrincipal{}, fmt.Errorf("commit GitHub identity lookup: %w", err)
		}
		return principal, nil
	}

	// A new user's tenant is unique to their immutable GitHub subject.
	// The mutable username remains display metadata only.
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = strings.TrimSpace(profile.Login)
	}
	if name == "" {
		name = "GitHub account"
	}
	principal = githubOAuthPrincipal{
		UserID: uuid.NewString(),
		OrgID:  uuid.NewString(),
		Role:   models.RoleOwner,
	}
	now := time.Now().UTC()
	slug := "github-user-" + providerID
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO organizations (id, name, slug, plan, settings, created_at, updated_at)
        VALUES ($1, $2, $3, 'free', '{}', $4, $4)
    `, principal.OrgID, name, slug, now); err != nil {
		// Existing slug or concurrent provisioning cannot grant membership.
		return githubOAuthPrincipal{}, fmt.Errorf("create GitHub organization: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO users (
            id, organization_id, email, name, avatar_url, role,
            github_id, github_username, settings, created_at, updated_at
        ) VALUES ($1, $2, $3, $4, $5, 'owner', $6, $7, '{}', $8, $8)
    `, principal.UserID, principal.OrgID, profile.Email, name, profile.AvatarURL,
		providerID, profile.Login, now); err != nil {
		return githubOAuthPrincipal{}, fmt.Errorf("create GitHub user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return githubOAuthPrincipal{}, fmt.Errorf("commit GitHub onboarding: %w", err)
	}
	return principal, nil
}
