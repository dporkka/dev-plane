package agentrunner

import (
	"errors"

	"github.com/ai-dev-control-plane/models"
)

// validateAgentExecutionScope refuses to authorize tools for an agent whose
// run/task/workspace identities disagree. Organization lookup must succeed
// before a decision can be made on the agent's behalf.
func validateAgentExecutionScope(run *models.AgentRun, task *models.Task, workspace *models.Workspace, organization *models.Organization) error {
	if run == nil || task == nil || workspace == nil || organization == nil ||
		run.ID == "" || task.ID == "" || task.ProjectID == "" ||
		task.RepositoryID == "" || workspace.ID == "" || organization.ID == "" {
		return errors.New("missing agent execution identity or organization")
	}
	if run.TaskID != task.ID {
		return errors.New("agent run task mismatch")
	}
	if workspace.RepositoryID != task.RepositoryID {
		return errors.New("agent workspace repository mismatch")
	}
	if run.WorkspaceID != nil && *run.WorkspaceID != workspace.ID {
		return errors.New("agent run workspace mismatch")
	}
	if workspace.TaskID != nil && *workspace.TaskID != task.ID {
		return errors.New("agent workspace task mismatch")
	}
	return nil
}
