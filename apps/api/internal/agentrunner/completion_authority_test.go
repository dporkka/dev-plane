package agentrunner

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/scheduler"
)

type recordingCompletionObserver struct {
	runID    string
	revision string
	evidence []scheduler.Evidence
}

func (o *recordingCompletionObserver) RecordRunCompletion(_ context.Context, runID, revision string, evidence []scheduler.Evidence) error {
	o.runID = runID
	o.revision = revision
	o.evidence = append([]scheduler.Evidence(nil), evidence...)
	return nil
}

func TestVerifyRunCompletionUsesCanonicalChecksWithoutChangingRunStatus(t *testing.T) {
	db := setupCompletionAuthorityDB(t)
	defer db.Close()

	sessionID := "runtime-1"
	provider := &fakeRuntimeProvider{
		commandResults: []*runtimes.CommandResult{
			{Stdout: "tree-a\n", ExitCode: 0},
			{Stdout: "ok\n", ExitCode: 0},
			{Stdout: "tree-a\n", ExitCode: 0},
		},
	}
	observer := &recordingCompletionObserver{}
	runner := NewRunner(db, tools.NewWorkspaceTools(nil), policies.NewEngine([]policies.Policy{
		{Name: "allow_completion_verification", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	}), nil, nil, nil).
		WithRuntimeProvider("docker", provider).
		WithCompletionObserver(observer)

	result, err := runner.VerifyRunCompletion(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("VerifyRunCompletion() error = %v", err)
	}
	if result.SubjectRevision != "git-tree:tree-a" {
		t.Fatalf("subject revision = %q", result.SubjectRevision)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].Name != "tests" {
		t.Fatalf("evidence = %#v", result.Evidence)
	}
	if observer.runID != "run-1" || observer.revision != "git-tree:tree-a" || len(observer.evidence) != 1 {
		t.Fatalf("observer = run:%q revision:%q evidence:%#v", observer.runID, observer.revision, observer.evidence)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-1'`).Scan(&status); err != nil {
		t.Fatalf("load run status: %v", err)
	}
	if status != "running" {
		t.Fatalf("run status = %q, want unchanged running", status)
	}

	if len(provider.commands) != 3 ||
		provider.commands[0].Command == "" ||
		provider.commands[1].Command != "go test ./..." ||
		provider.commands[2].Command == "" {
		t.Fatalf("verification commands = %#v", provider.commands)
	}
	_ = sessionID
}

func TestVerifyRunCompletionRequiresEvidenceObserver(t *testing.T) {
	db := setupCompletionAuthorityDB(t)
	defer db.Close()

	provider := &fakeRuntimeProvider{
		commandResults: []*runtimes.CommandResult{
			{Stdout: "tree-a\n", ExitCode: 0},
			{Stdout: "ok\n", ExitCode: 0},
			{Stdout: "tree-a\n", ExitCode: 0},
		},
	}
	runner := NewRunner(db, tools.NewWorkspaceTools(nil), policies.NewEngine([]policies.Policy{
		{Name: "allow_completion_verification", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	}), nil, nil, nil).WithRuntimeProvider("docker", provider)

	_, err := runner.VerifyRunCompletion(context.Background(), "run-1")
	if err == nil {
		t.Fatal("VerifyRunCompletion() error = nil, want missing observer failure")
	}
}

func setupCompletionAuthorityDB(t *testing.T) *sql.DB {
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
		);
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			repository_id TEXT NOT NULL,
			workspace_id TEXT,
			created_by TEXT NOT NULL,
			source TEXT NOT NULL,
			source_id TEXT,
			title TEXT NOT NULL,
			description TEXT,
			status TEXT NOT NULL,
			priority TEXT NOT NULL,
			risk_level TEXT NOT NULL,
			target_branch TEXT NOT NULL,
			spec TEXT,
			acceptance_criteria TEXT,
			max_cost REAL,
			max_runtime_minutes INTEGER,
			approval_requirements TEXT,
			metadata TEXT,
			started_at DATETIME,
			completed_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			task_id TEXT,
			name TEXT NOT NULL,
			branch TEXT NOT NULL,
			base_branch TEXT NOT NULL,
			worktree_path TEXT,
			runtime_provider TEXT NOT NULL,
			runtime_session_id TEXT,
			status TEXT NOT NULL,
			preview_url TEXT,
			settings TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT,
			agent_role TEXT NOT NULL,
			model TEXT,
			provider TEXT,
			status TEXT NOT NULL,
			started_at DATETIME,
			completed_at DATETIME,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_cost REAL NOT NULL DEFAULT 0,
			error_message TEXT,
			summary TEXT,
			metadata TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);

		INSERT INTO project_configs (
			id, repository_id, test_command, lint_command, typecheck_command, build_command, updated_at
		) VALUES (
			'config-1', 'repo-1', 'go test ./...', '', '', '', CURRENT_TIMESTAMP
		);
		INSERT INTO tasks (
			id, project_id, repository_id, workspace_id, created_by, source, title,
			status, priority, risk_level, target_branch, max_runtime_minutes, created_at, updated_at
		) VALUES (
			'task-1', 'project-1', 'repo-1', 'workspace-1', 'user-1', 'manual', 'test',
			'running', 'normal', 'low', 'main', 30, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		);
		INSERT INTO workspaces (
			id, repository_id, task_id, name, branch, base_branch, runtime_provider,
			runtime_session_id, status, created_at, updated_at
		) VALUES (
			'workspace-1', 'repo-1', 'task-1', 'test', 'agent/run-1', 'main', 'docker',
			'runtime-1', 'ready', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		);
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, metadata, created_at, updated_at
		) VALUES (
			'run-1', 'task-1', 'workspace-1', 'implementer', 'gpt-5.6-codex', 'codex',
			'running', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create completion authority fixture: %v", err)
	}
	return db
}
