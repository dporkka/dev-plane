package agentexecutor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/prfactory"
	"github.com/ai-dev-control-plane/runtimes"
)

var errRunRepositoryAuthority = errors.New("run repository authority is unavailable")

type runAuthority struct {
	metadata, taskID, repositoryID, projectID                                                                  string
	settings, runWorkspace, taskWorkspace, workspaceTask, workspaceRepository, provider, session, path, status sql.NullString
}

// Resolve durable run -> task -> repository -> tenant. The event metadata is
// not authority. Deleted or mismatched projects cannot launch any backend.
func loadRunAuthority(ctx context.Context, db *sql.DB, runID string) (runAuthority, error) {
	var a runAuthority
	err := db.QueryRowContext(ctx, `
    SELECT COALESCE(ar.metadata,'{}'),t.id,r.id,p.id,r.settings,ar.workspace_id,t.workspace_id,
           w.task_id,w.repository_id,w.runtime_provider,w.runtime_session_id,w.worktree_path,w.status
    FROM agent_runs ar
    JOIN tasks t ON t.id=ar.task_id AND t.deleted_at IS NULL
    JOIN repositories r ON r.id=t.repository_id AND r.project_id=t.project_id AND r.deleted_at IS NULL
    JOIN projects p ON p.id=t.project_id AND p.deleted_at IS NULL
    JOIN organizations o ON o.id=p.organization_id AND o.deleted_at IS NULL
    LEFT JOIN workspaces w ON w.id=ar.workspace_id AND w.deleted_at IS NULL
    WHERE ar.id=$1
 `, runID).Scan(&a.metadata, &a.taskID, &a.repositoryID, &a.projectID, &a.settings, &a.runWorkspace, &a.taskWorkspace,
		&a.workspaceTask, &a.workspaceRepository, &a.provider, &a.session, &a.path, &a.status)
	if err != nil {
		return runAuthority{}, fmt.Errorf("%w: active run/task/project/repository unavailable", errRunRepositoryAuthority)
	}
	return a, nil
}

type workspaceVerifier func(context.Context, string, runtimes.GiteaWorkspaceGrant) error

func (a runAuthority) authorize(ctx context.Context, db *sql.DB, backend string, verify workspaceVerifier) error {
	settings, err := models.ParseRepositoryForgeSettings([]byte(a.settings.String))
	if err != nil {
		return errRunRepositoryAuthority
	}
	if settings.Provider != models.ForgeProviderGitea {
		// Local/legacy and external agents must not share a process containing
		// Gitea operator credentials. A dedicated Gitea worker is required.
		if prfactory.GiteaCredentialsConfigured() {
			return runtimes.ErrGiteaWorkspaceAuthority
		}
		return nil
	}
	// External agents and old host worktrees have not been qualified. Require
	// the precise task-owned isolated workspace and a matching actual container.
	if backend != executionBackendLegacy || !a.runWorkspace.Valid || a.runWorkspace.String == "" ||
		a.runWorkspace != a.taskWorkspace || a.workspaceTask.String != a.taskID ||
		a.workspaceRepository.String != a.repositoryID || a.provider.String != "docker" ||
		!a.session.Valid || a.session.String == "" || a.path.String != "" ||
		(a.status.String != "ready" && a.status.String != "running") {
		return runtimes.ErrGiteaWorkspaceAuthority
	}
	forge, credential, err := prfactory.ConfiguredGiteaBindingForge()
	if err != nil {
		return err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	binding, err := prfactory.VerifyGiteaRepositoryBinding(verifyCtx, db, forge, credential, a.repositoryID, a.projectID)
	if err != nil {
		return err
	}
	return verify(verifyCtx, a.session.String, runtimes.GiteaWorkspaceGrant{
		RepositoryID: binding.RepositoryID, WorkspaceID: a.runWorkspace.String, InstanceURL: binding.InstanceURL,
		CloneURL: binding.CloneURL, UpstreamRepositoryID: binding.UpstreamRepositoryID,
	})
}
