package agentrunner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/runtimes"
)

func TestValidateAgentExecutionScope(t *testing.T) {
	workspaceID := "ws-1"
	taskID := "task-1"
	valid := func() (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) {
		return &models.AgentRun{ID: "run-1", TaskID: taskID, WorkspaceID: &workspaceID},
			&models.Task{ID: taskID, ProjectID: "project-1", RepositoryID: "repo-1"},
			&models.Workspace{ID: workspaceID, RepositoryID: "repo-1", TaskID: &taskID},
			&models.Organization{ID: "org-1"}
	}
	tests := []struct {
		name   string
		change func(*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization)
		reject bool
	}{
		{"matching identities", func(a *models.AgentRun, t *models.Task, w *models.Workspace, o *models.Organization) (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) {
			return a, t, w, o
		}, false},
		{"missing organization", func(a *models.AgentRun, t *models.Task, w *models.Workspace, o *models.Organization) (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) {
			return a, t, w, nil
		}, true},
		{"different task", func(a *models.AgentRun, t *models.Task, w *models.Workspace, o *models.Organization) (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) {
			a.TaskID = "task-2"
			return a, t, w, o
		}, true},
		{"different repository", func(a *models.AgentRun, t *models.Task, w *models.Workspace, o *models.Organization) (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) {
			w.RepositoryID = "repo-2"
			return a, t, w, o
		}, true},
		{"different run workspace", func(a *models.AgentRun, t *models.Task, w *models.Workspace, o *models.Organization) (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) {
			a.WorkspaceID = new(string)
			*a.WorkspaceID = "ws-2"
			return a, t, w, o
		}, true},
		{"different workspace task", func(a *models.AgentRun, t *models.Task, w *models.Workspace, o *models.Organization) (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) {
			w.TaskID = new(string)
			*w.TaskID = "task-2"
			return a, t, w, o
		}, true},
		{"missing project", func(a *models.AgentRun, t *models.Task, w *models.Workspace, o *models.Organization) (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) {
			t.ProjectID = ""
			return a, t, w, o
		}, true},
		{"blank organization", func(a *models.AgentRun, t *models.Task, w *models.Workspace, o *models.Organization) (*models.AgentRun, *models.Task, *models.Workspace, *models.Organization) {
			o.ID = ""
			return a, t, w, o
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, task, w, o := tt.change(valid())
			err := validateAgentExecutionScope(a, task, w, o)
			if (err != nil) != tt.reject {
				t.Fatalf("err=%v reject=%v", err, tt.reject)
			}
		})
	}
}

// An invalid scope must be rejected before any revision-capture command,
// including git add -A and configured Git filters, is executed.
func TestAuthorizeToolRejectsMismatchedScopeBeforeRevisionCapture(t *testing.T) {
	db := openAuthorityConsumptionDB(t)
	provider := &fakeRuntimeProvider{commandResult: &runtimes.CommandResult{Stdout: "tree-hash", ExitCode: 0}}
	r := &Runner{db: db, kernel: capability.NewKernel(policies.DefaultEngine(), nil, nil, nil),
		runtimes: map[string]runtimes.Provider{"docker": provider}}
	session := "sandbox"
	workspace := &models.Workspace{ID: "workspace-A", RepositoryID: "repo-A",
		RuntimeProvider: "docker", RuntimeSessionID: &session}
	run := &models.AgentRun{ID: "run-A", TaskID: "another-task", AgentRole: "implementer"}
	task := &models.Task{ID: "task-A", ProjectID: "project-1", RepositoryID: "repo-A"}
	err := r.authorizeTool(context.Background(), run, task, workspace, "", "write_file",
		json.RawMessage(`{"path":"a.go","content":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid agent execution scope") {
		t.Fatalf("missing scope validation: %v", err)
	}
	if len(provider.commands) != 0 {
		t.Fatalf("revision capture executed before scope check: %d", len(provider.commands))
	}
}
