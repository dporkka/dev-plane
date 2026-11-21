package prfactory

import (
	"errors"

	"github.com/ai-dev-control-plane/models"
)

// validateFactoryWorkspace refuses to use a run or worktree from another task
// or repository. The repository-to-project check is enforced separately in SQL.
func validateFactoryWorkspace(task *models.Task, run *models.AgentRun, workspace *models.Workspace) error {
	if task == nil || run == nil || workspace == nil ||
		task.ID == "" || task.ProjectID == "" || task.RepositoryID == "" ||
		run.ID == "" || workspace.ID == "" || workspace.RepositoryID == "" {
		return errors.New("missing PR factory execution identity")
	}
	if run.TaskID != task.ID || run.WorkspaceID == nil || *run.WorkspaceID != workspace.ID ||
		workspace.RepositoryID != task.RepositoryID {
		return errors.New("PR factory run or repository identity mismatch")
	}
	if workspace.TaskID != nil && *workspace.TaskID != task.ID {
		return errors.New("PR factory workspace is bound to a different task")
	}
	return nil
}
