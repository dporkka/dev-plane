package agentrunner

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/scheduler"
)

func TestRunFinalChecksUsesConfiguredPlanAndBindsEvidenceToTree(t *testing.T) {
	db := setupFinalCheckDB(t)
	defer db.Close()
	_, err := db.Exec(`
		INSERT INTO project_configs (
			id, repository_id, test_command, lint_command, typecheck_command, build_command, updated_at
		) VALUES (
			'config-1', 'repo-1', 'go test ./...', 'go vet ./...', '', '', CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("insert project config: %v", err)
	}

	sessionID := "runtime-1"
	provider := &fakeRuntimeProvider{
		commandResults: []*runtimes.CommandResult{
			{Stdout: "tree-a\n", ExitCode: 0},
			{Stdout: "ok  \texample\t0.1s\n", ExitCode: 0},
			{Stdout: "", ExitCode: 0},
			{Stdout: "tree-a\n", ExitCode: 0},
		},
	}
	runner := NewRunner(db, tools.NewWorkspaceTools(nil), policies.NewEngine([]policies.Policy{
		{Name: "allow_final_checks", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	}), nil, nil, nil).
		WithRuntimeProvider("docker", provider).
		WithCompletionObserver(noopCompletionObserver{})

	workspace := &models.Workspace{
		ID:               "workspace-1",
		RepositoryID:     "repo-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
		Status:           models.WorkspaceStatusReady,
	}
	run := &models.AgentRun{ID: "run-1", AgentRole: models.AgentRoleImplementer}
	task := &models.Task{ID: "task-1", RepositoryID: "repo-1"}

	report, err := runner.runFinalChecks(context.Background(), run, task, workspace, "")
	if err != nil {
		t.Fatalf("runFinalChecks() error: %v", err)
	}
	if report.SubjectRevision != "git-tree:tree-a" {
		t.Fatalf("subject revision = %q, want git-tree:tree-a", report.SubjectRevision)
	}
	if len(report.Evidence) != 2 {
		t.Fatalf("evidence = %#v, want 2 checks", report.Evidence)
	}
	wantNames := []string{"tests", "lint"}
	for i, want := range wantNames {
		if report.Evidence[i].Name != want {
			t.Fatalf("evidence[%d].Name = %q, want %q", i, report.Evidence[i].Name, want)
		}
		if report.Evidence[i].Status != scheduler.EvidenceStatusPassed {
			t.Fatalf("evidence[%d].Status = %q, want passed", i, report.Evidence[i].Status)
		}
		if report.Evidence[i].SubjectRevision != "git-tree:tree-a" {
			t.Fatalf("evidence[%d].SubjectRevision = %q", i, report.Evidence[i].SubjectRevision)
		}
	}
	if len(provider.commands) != 4 {
		t.Fatalf("commands = %#v, want tree + tests + lint + tree", provider.commands)
	}
	if !strings.Contains(provider.commands[0].Command, "git write-tree") ||
		provider.commands[1].Command != "go test ./..." ||
		provider.commands[2].Command != "go vet ./..." ||
		!strings.Contains(provider.commands[3].Command, "git write-tree") {
		t.Fatalf("unexpected command sequence: %#v", provider.commands)
	}
}

func TestRunFinalChecksRejectsWorkspaceMutationDuringVerification(t *testing.T) {
	db := setupFinalCheckDB(t)
	defer db.Close()
	_, err := db.Exec(`
		INSERT INTO project_configs (
			id, repository_id, test_command, lint_command, typecheck_command, build_command, updated_at
		) VALUES (
			'config-1', 'repo-1', 'go test ./...', '', '', '', CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("insert project config: %v", err)
	}

	sessionID := "runtime-1"
	provider := &fakeRuntimeProvider{
		commandResults: []*runtimes.CommandResult{
			{Stdout: "tree-before\n", ExitCode: 0},
			{Stdout: "ok  \texample\t0.1s\n", ExitCode: 0},
			{Stdout: "tree-after\n", ExitCode: 0},
		},
	}
	runner := NewRunner(db, tools.NewWorkspaceTools(nil), policies.NewEngine([]policies.Policy{
		{Name: "allow_final_checks", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	}), nil, nil, nil).
		WithRuntimeProvider("docker", provider).
		WithCompletionObserver(noopCompletionObserver{})

	workspace := &models.Workspace{
		ID:               "workspace-1",
		RepositoryID:     "repo-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
		Status:           models.WorkspaceStatusReady,
	}
	run := &models.AgentRun{ID: "run-1", AgentRole: models.AgentRoleImplementer}
	task := &models.Task{ID: "task-1", RepositoryID: "repo-1"}

	_, err = runner.runFinalChecks(context.Background(), run, task, workspace, "")
	if err == nil || !strings.Contains(err.Error(), "workspace changed during final verification") {
		t.Fatalf("error = %v, want workspace mutation error", err)
	}
}

func setupFinalCheckDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE project_configs (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			test_command TEXT,
			lint_command TEXT,
			typecheck_command TEXT,
			build_command TEXT,
			updated_at DATETIME
		)
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create project_configs: %v", err)
	}
	return db
}


type noopCompletionObserver struct{}

func (noopCompletionObserver) RecordRunCompletion(
	context.Context,
	string,
	string,
	[]scheduler.Evidence,
) error {
	return nil
}
