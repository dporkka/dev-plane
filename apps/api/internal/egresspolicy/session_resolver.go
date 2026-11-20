package egresspolicy

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/ai-dev-control-plane/api/internal/auth"
)

// authorizedRunKey is private: untrusted HTTP payload values must never be
// treated as a permission to execute a run.
type authorizedRunKey struct{}

// WithAuthorizedRun tags a server-authenticated request context with the
// agent run selected by the trusted scheduler. This alone is NOT authority:
// the resolver verifies active run ownership and the caller's current
// organization membership against the database on every fetch.
func WithAuthorizedRun(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, authorizedRunKey{}, runID)
}

// DatabaseSessionResolver binds a user-authenticated, run-scoped operation to a
// single active Docker runtime session. No endpoint is registered here.
type DatabaseSessionResolver struct{ db *sql.DB }

func NewDatabaseSessionResolver(db *sql.DB) *DatabaseSessionResolver {
	return &DatabaseSessionResolver{db: db}
}

func (r *DatabaseSessionResolver) ResolveAuthorizedSession(ctx context.Context, sessionID string) (SessionIdentity, error) {
	denied := SessionIdentity{}
	if r == nil || r.db == nil || ctx == nil || ctx.Err() != nil || sessionID == "" ||
		strings.TrimSpace(sessionID) != sessionID {
		return denied, ErrDenied
	}
	user := auth.UserFromContext(ctx)
	runID, _ := ctx.Value(authorizedRunKey{}).(string)
	if user == nil || user.UserID == "" || user.OrgID == "" || runID == "" ||
		strings.TrimSpace(user.UserID) != user.UserID || strings.TrimSpace(user.OrgID) != user.OrgID ||
		strings.TrimSpace(runID) != runID {
		return denied, ErrDenied
	}

	// Verify that the *same* active agent run belongs to the same task,
	// workspace, repository/project and authenticated organization. A regular
	// member may only use runs for tasks they created; owner/admin may operate
	// organization runs. Authorization uses the current database role. A deleted
	// user, task, repo, project or workspace cannot yield an egress capability.
	// DB is the source of truth; client-supplied tenant/agent IDs are ignored.
	const stmt = `
SELECT ar.id, p.organization_id
FROM workspaces w
JOIN agent_runs ar ON ar.workspace_id = w.id AND ar.task_id = w.task_id
JOIN tasks t ON t.id = ar.task_id AND t.deleted_at IS NULL
JOIN repositories repo ON repo.id = w.repository_id
  AND repo.id = t.repository_id AND repo.deleted_at IS NULL
JOIN projects p ON p.id = t.project_id AND repo.project_id = p.id
  AND p.deleted_at IS NULL
JOIN users u ON u.id = $3 AND u.organization_id = p.organization_id
  AND u.deleted_at IS NULL
  AND (t.created_by = u.id OR u.role IN ('owner','admin'))
WHERE w.runtime_session_id = $1 AND ar.id = $2
  AND p.organization_id = $4
  AND w.deleted_at IS NULL
  AND w.runtime_provider = 'docker'
  AND w.status IN ('ready','running')
  AND ar.status = 'running'
LIMIT 1
`
	var activeRun, tenant string
	if err := r.db.QueryRowContext(ctx, stmt, sessionID, runID, user.UserID, user.OrgID).Scan(&activeRun, &tenant); err != nil {
		return denied, ErrDenied
	}
	if activeRun != runID || tenant != user.OrgID {
		return denied, ErrDenied
	}
	return SessionIdentity{RuntimeSessionID: sessionID, TenantID: tenant, AgentID: activeRun}, nil
}
