// Package authz provides resource-level authorization helpers for API handlers.
//
// Every helper verifies that the authenticated user's organization matches the
// organization that owns the requested resource. When the resource does not
// exist or the user does not belong to its owning organization, the helper
// returns an error. Callers should treat all errors as "not found" (HTTP 404)
// to avoid leaking the existence of resources across organizations.
package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/ai-dev-control-plane/api/internal/auth"
)

// ErrNotFound is returned when a resource does not exist or the caller is not
// authorized to access it. It intentionally collapses both cases so handlers
// can return a single 404 response.
var ErrNotFound = errors.New("resource not found")

// UserFromRequest extracts authenticated user claims from the request context.
func UserFromRequest(r *http.Request) (*auth.Claims, error) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		return nil, errors.New("unauthorized")
	}
	return user, nil
}

// RequireUser returns the authenticated user or writes a 401 response and
// returns false. It is a convenience wrapper for handlers.
func RequireUser(w http.ResponseWriter, r *http.Request) (*auth.Claims, bool) {
	user, err := UserFromRequest(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return nil, false
	}
	return user, true
}

// AuthorizeOrganization verifies that the user belongs to the organization.
func AuthorizeOrganization(ctx context.Context, db *sql.DB, user *auth.Claims, orgID string) error {
	if user.OrgID == "" {
		return ErrNotFound
	}
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		)
	`, user.UserID, orgID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("authorize organization: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// AuthorizeProject verifies that the user's organization owns the project.
func AuthorizeProject(ctx context.Context, db *sql.DB, user *auth.Claims, projectID string) error {
	return authorizeByColumn(ctx, db, user, "projects", "id", projectID)
}

// AuthorizeRepository verifies that the user's organization owns the repository.
func AuthorizeRepository(ctx context.Context, db *sql.DB, user *auth.Claims, repoID string) error {
	var orgID string
	err := db.QueryRowContext(ctx, `
		SELECT p.organization_id
		FROM repositories r
		JOIN projects p ON p.id = r.project_id
		WHERE r.id = $1 AND r.deleted_at IS NULL AND p.deleted_at IS NULL
	`, repoID).Scan(&orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("authorize repository: %w", err)
	}
	if orgID != user.OrgID {
		return ErrNotFound
	}
	return nil
}

// AuthorizeTask verifies that the user's organization owns the task.
func AuthorizeTask(ctx context.Context, db *sql.DB, user *auth.Claims, taskID string) error {
	var orgID string
	err := db.QueryRowContext(ctx, `
		SELECT p.organization_id
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		WHERE t.id = $1 AND t.deleted_at IS NULL AND p.deleted_at IS NULL
	`, taskID).Scan(&orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("authorize task: %w", err)
	}
	if orgID != user.OrgID {
		return ErrNotFound
	}
	return nil
}

// AuthorizeWorkspace verifies that the user's organization owns the workspace.
func AuthorizeWorkspace(ctx context.Context, db *sql.DB, user *auth.Claims, workspaceID string) error {
	var orgID string
	err := db.QueryRowContext(ctx, `
		SELECT p.organization_id
		FROM workspaces w
		JOIN repositories r ON r.id = w.repository_id
		JOIN projects p ON p.id = r.project_id
		WHERE w.id = $1 AND r.deleted_at IS NULL AND p.deleted_at IS NULL
	`, workspaceID).Scan(&orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("authorize workspace: %w", err)
	}
	if orgID != user.OrgID {
		return ErrNotFound
	}
	return nil
}

// AuthorizeAgentRun verifies that the user's organization owns the agent run.
func AuthorizeAgentRun(ctx context.Context, db *sql.DB, user *auth.Claims, runID string) error {
	var orgID string
	err := db.QueryRowContext(ctx, `
		SELECT p.organization_id
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE ar.id = $1 AND t.deleted_at IS NULL AND p.deleted_at IS NULL
	`, runID).Scan(&orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("authorize agent run: %w", err)
	}
	if orgID != user.OrgID {
		return ErrNotFound
	}
	return nil
}

// AuthorizeApproval verifies that the user's organization owns the approval.
func AuthorizeApproval(ctx context.Context, db *sql.DB, user *auth.Claims, approvalID string) error {
	var orgID string
	err := db.QueryRowContext(ctx, `
		SELECT p.organization_id
		FROM approvals a
		JOIN tasks t ON t.id = a.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE a.id = $1 AND t.deleted_at IS NULL AND p.deleted_at IS NULL
	`, approvalID).Scan(&orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("authorize approval: %w", err)
	}
	if orgID != user.OrgID {
		return ErrNotFound
	}
	return nil
}

// AuthorizePullRequest verifies that the user's organization owns the pull request.
func AuthorizePullRequest(ctx context.Context, db *sql.DB, user *auth.Claims, prID string) error {
	var orgID string
	err := db.QueryRowContext(ctx, `
		SELECT p.organization_id
		FROM pull_requests pr
		JOIN repositories r ON r.id = pr.repository_id
		JOIN projects p ON p.id = r.project_id
		WHERE pr.id = $1 AND r.deleted_at IS NULL AND p.deleted_at IS NULL
	`, prID).Scan(&orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("authorize pull request: %w", err)
	}
	if orgID != user.OrgID {
		return ErrNotFound
	}
	return nil
}

// AuthorizeSecret verifies that the user's organization owns the secret reference.
func AuthorizeSecret(ctx context.Context, db *sql.DB, user *auth.Claims, secretID string) error {
	return authorizeByColumn(ctx, db, user, "secret_references", "id", secretID)
}

// AuthorizeIntegration verifies that the user's organization owns the integration.
func AuthorizeIntegration(ctx context.Context, db *sql.DB, user *auth.Claims, integrationID string) error {
	return authorizeByColumn(ctx, db, user, "integrations", "id", integrationID)
}

// AuthorizePolicy verifies that the user's organization owns the policy.
func AuthorizePolicy(ctx context.Context, db *sql.DB, user *auth.Claims, policyID string) error {
	return authorizeByColumn(ctx, db, user, "policies", "id", policyID)
}

// AuthorizeArtifact verifies that the user's organization owns the artifact.
func AuthorizeArtifact(ctx context.Context, db *sql.DB, user *auth.Claims, artifactID string) error {
	return authorizeByColumn(ctx, db, user, "artifacts", "id", artifactID)
}

// authorizeByColumn is a generic helper for tables that have an
// organization_id column directly.
func authorizeByColumn(ctx context.Context, db *sql.DB, user *auth.Claims, table, column, value string) error {
	var orgID string
	query := fmt.Sprintf(`
		SELECT organization_id
		FROM %s
		WHERE %s = $1
	`, table, column)
	err := db.QueryRowContext(ctx, query, value).Scan(&orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("authorize %s: %w", table, err)
	}
	if orgID != user.OrgID {
		return ErrNotFound
	}
	return nil
}
