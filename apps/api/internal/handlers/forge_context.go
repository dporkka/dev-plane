package handlers

import (
	"context"
	"database/sql"

	"github.com/ai-dev-control-plane/models"
)

type forgeContext struct {
	Task         *models.Task
	Run          *models.AgentRun
	Repository   *models.Repository
	Project      *models.Project
	Organization *models.Organization
	Workspace    *models.Workspace
}

func (h *Handler) loadForgeContext(ctx context.Context, taskID, runID string) (*forgeContext, error) {
	var (
		taskWorkspaceID sql.NullString
		runWorkspaceID  sql.NullString
		startedAt       sql.NullTime
		completedAt     sql.NullTime
		workspaceID     sql.NullString
		workspaceBranch sql.NullString
		runtimeProvider sql.NullString
		task            models.Task
		run             models.AgentRun
		repo            models.Repository
		orgID           string
	)

	err := h.db.QueryRowContext(ctx, `
		SELECT
			t.id, t.project_id, t.repository_id, t.workspace_id, t.created_by, t.risk_level, t.target_branch,
			ar.id, ar.workspace_id, ar.agent_role, ar.status,
			ar.prompt_tokens, ar.completion_tokens, ar.total_cost, ar.started_at, ar.completed_at,
			r.id, r.project_id, r.owner, r.name, r.full_name, r.default_branch,
			p.organization_id,
			w.id, w.branch, w.runtime_provider
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		JOIN repositories r ON r.id = t.repository_id
		JOIN projects p ON p.id = t.project_id
		LEFT JOIN workspaces w ON w.id = COALESCE(ar.workspace_id, t.workspace_id)
		WHERE ar.id = $1
		  AND t.id = $2
		  AND t.deleted_at IS NULL
		  AND r.deleted_at IS NULL
		  AND p.deleted_at IS NULL
	`, runID, taskID).Scan(
		&task.ID, &task.ProjectID, &task.RepositoryID, &taskWorkspaceID, &task.CreatedBy, &task.RiskLevel, &task.TargetBranch,
		&run.ID, &runWorkspaceID, &run.AgentRole, &run.Status,
		&run.PromptTokens, &run.CompletionTokens, &run.TotalCost, &startedAt, &completedAt,
		&repo.ID, &repo.ProjectID, &repo.Owner, &repo.Name, &repo.FullName, &repo.DefaultBranch,
		&orgID,
		&workspaceID, &workspaceBranch, &runtimeProvider,
	)
	if err != nil {
		return nil, err
	}

	run.TaskID = task.ID
	if taskWorkspaceID.Valid {
		id := taskWorkspaceID.String
		task.WorkspaceID = &id
	}
	if runWorkspaceID.Valid {
		id := runWorkspaceID.String
		run.WorkspaceID = &id
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}

	out := &forgeContext{
		Task:         &task,
		Run:          &run,
		Repository:   &repo,
		Project:      &models.Project{ID: task.ProjectID, OrganizationID: orgID},
		Organization: &models.Organization{ID: orgID},
	}
	if workspaceID.Valid {
		out.Workspace = &models.Workspace{
			ID:              workspaceID.String,
			RepositoryID:    repo.ID,
			Branch:          workspaceBranch.String,
			RuntimeProvider: runtimeProvider.String,
		}
	}
	return out, nil
}
