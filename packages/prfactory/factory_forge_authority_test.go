package prfactory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/gateway"
)

func TestCreatePullRequestRejectsRepositoryForgeMismatchBeforePublication(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id").
		WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "repository_id", "workspace_id", "created_by", "source", "source_id",
			"title", "description", "status", "priority", "risk_level", "target_branch", "spec",
			"acceptance_criteria", "max_cost", "max_runtime_minutes", "approval_requirements", "metadata",
			"started_at", "completed_at", "created_at", "updated_at",
		}).AddRow(
			"task-1", "project-1", "repo-1", nil, "user-1", "manual", nil,
			"Change widget", nil, "approved", "medium", "low", "main", nil,
			nil, nil, 30, nil, nil, nil, nil, now, now,
		))

	mock.ExpectQuery("SELECT id, task_id, workspace_id, agent_role").
		WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "workspace_id", "agent_role", "model", "provider", "status",
			"prompt_tokens", "completion_tokens", "total_cost", "error_message", "summary", "metadata",
			"created_at", "updated_at",
		}).AddRow(
			"run-1", "task-1", nil, "coder", nil, nil, "completed",
			0, 0, 0.0, nil, nil, []byte(`{}`), now, now,
		))

	// Reviewer lookup is allowed to miss; Factory intentionally falls back to a
	// default review report. The repository authority lookup must then stop the
	// flow before Git push or CreatePullRequest on the configured forge.
	mock.ExpectQuery("SELECT owner, name, settings FROM repositories").
		WithArgs("repo-1").
		WillReturnRows(sqlmock.NewRows([]string{"owner", "name", "settings"}).AddRow(
			"acme", "widget", `{"forge":{"provider":"gitea","base_url":"https://git.example.test"}}`,
		))

	forge := &recordingForge{}
	factory := NewFactory(db, nil).WithForge(forge, gateway.ForgeCredential{AccessToken: "api-token"})
	_, err = factory.CreatePullRequest(context.Background(), "task-1")
	if err == nil || !strings.Contains(err.Error(), "repository requires forge provider gitea") {
		t.Fatalf("error = %v", err)
	}
	if forge.owner != "" || forge.name != "" {
		t.Fatalf("forge publication occurred unexpectedly for %s/%s", forge.owner, forge.name)
	}
}
