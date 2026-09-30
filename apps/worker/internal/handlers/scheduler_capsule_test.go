package handlers

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/scheduler"
)

func TestBuildAdmittedTaskCapsuleUsesSchedulerMetadataAndRunIdentity(t *testing.T) {
	db := setupAdmittedCapsuleDB(t)
	defer db.Close()

	_, err := db.Exec(`
		INSERT INTO tasks (
			id, project_id, repository_id, title, description, status, metadata, deleted_at
		) VALUES (
			'task-1', 'project-1', 'repo-1', 'Implement API change', 'Preserve behavior',
			'running',
			'{"scheduler":{"owns":["./apps/api/**","docs/*"],"depends_on":["task-dep"],"cpu":1,"memory_mb":512}}',
			NULL
		);
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, metadata, updated_at
		) VALUES (
			'run-1', 'task-1', 'workspace-1', 'implementer', 'gpt-5.6', 'openai',
			'admitting', '{}', CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 2, CPU: 4, MemoryMB: 4096})
	capsule, err := admission.BuildAdmittedTaskCapsule(
		context.Background(),
		"run-1",
		"task-1",
		[]string{"tests", "lint"},
	)
	if err != nil {
		t.Fatalf("BuildAdmittedTaskCapsule() error: %v", err)
	}

	if capsule.Version != scheduler.TaskCapsuleVersion {
		t.Fatalf("version = %d, want %d", capsule.Version, scheduler.TaskCapsuleVersion)
	}
	if capsule.TaskID != "task-1" || capsule.WorkspaceID != "workspace-1" {
		t.Fatalf("task/workspace = %q/%q", capsule.TaskID, capsule.WorkspaceID)
	}
	if capsule.Agent.ID != "run-1" ||
		capsule.Agent.Role != "implementer" ||
		capsule.Agent.Provider != "openai" ||
		capsule.Agent.Model != "gpt-5.6" {
		t.Fatalf("unexpected agent identity: %+v", capsule.Agent)
	}
	if capsule.Objective != "Implement API change" {
		t.Fatalf("objective = %q", capsule.Objective)
	}
	if !reflect.DeepEqual(capsule.DependsOn, []string{"task-dep"}) {
		t.Fatalf("dependencies = %#v", capsule.DependsOn)
	}
	wantLeases := []scheduler.Lease{
		{Path: "apps/api", Mode: scheduler.LeaseModeExclusive},
		{Path: "docs", Mode: scheduler.LeaseModeExclusive},
	}
	if !reflect.DeepEqual(capsule.Leases, wantLeases) {
		t.Fatalf("leases = %#v, want %#v", capsule.Leases, wantLeases)
	}
	if !reflect.DeepEqual(capsule.RequiredEvidence, []string{"tests", "lint"}) {
		t.Fatalf("required evidence = %#v", capsule.RequiredEvidence)
	}
}

func TestBuildAdmittedTaskCapsuleDerivesRequiredEvidenceFromProjectConfig(t *testing.T) {
	db := setupAdmittedCapsuleDB(t)
	defer db.Close()

	_, err := db.Exec(`
		INSERT INTO tasks (
			id, project_id, repository_id, title, description, status, metadata, deleted_at
		) VALUES (
			'task-1', 'project-1', 'repo-1', 'Implement API change', '',
			'running', '{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}', NULL
		);
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, metadata, updated_at
		) VALUES (
			'run-1', 'task-1', 'workspace-1', 'implementer', 'gpt-5.6', 'openai',
			'admitting', '{}', CURRENT_TIMESTAMP
		);
		INSERT INTO project_configs (
			id, repository_id, test_command, lint_command, typecheck_command, build_command, updated_at
		) VALUES (
			'config-1', 'repo-1', 'go test ./...', '', 'go vet ./...', 'go build ./...', CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	admission := NewSchedulerAdmission(db, SchedulerCapacity{})
	capsule, err := admission.BuildAdmittedTaskCapsule(context.Background(), "run-1", "task-1", nil)
	if err != nil {
		t.Fatalf("BuildAdmittedTaskCapsule() error: %v", err)
	}
	want := []string{"tests", "typecheck", "build"}
	if !reflect.DeepEqual(capsule.RequiredEvidence, want) {
		t.Fatalf("required evidence = %#v, want %#v", capsule.RequiredEvidence, want)
	}
}

func TestBuildAdmittedTaskCapsuleRejectsQueuedRun(t *testing.T) {
	db := setupAdmittedCapsuleDB(t)
	defer db.Close()

	_, err := db.Exec(`
		INSERT INTO tasks (
			id, project_id, repository_id, title, description, status, metadata, deleted_at
		) VALUES (
			'task-1', 'project-1', 'repo-1', 'Implement API change', '',
			'running',
			'{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}',
			NULL
		);
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, metadata, updated_at
		) VALUES (
			'run-1', 'task-1', 'workspace-1', 'implementer', 'gpt-5.6', 'openai',
			'queued', '{}', CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	admission := NewSchedulerAdmission(db, SchedulerCapacity{})
	_, err = admission.BuildAdmittedTaskCapsule(context.Background(), "run-1", "task-1", []string{"tests"})
	if err == nil || !strings.Contains(err.Error(), "not admitted") {
		t.Fatalf("expected not admitted error, got %v", err)
	}
}

func TestBuildAdmittedTaskCapsuleSupportsLegacyRunWithoutOwnership(t *testing.T) {
	db := setupAdmittedCapsuleDB(t)
	defer db.Close()

	_, err := db.Exec(`
		INSERT INTO tasks (
			id, project_id, repository_id, title, description, status, metadata, deleted_at
		) VALUES (
			'task-legacy', 'project-1', 'repo-1', 'Legacy task', '',
			'running', '{}', NULL
		);
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, metadata, updated_at
		) VALUES (
			'run-legacy', 'task-legacy', 'workspace-legacy', 'implementer', NULL, NULL,
			'admitting', '{}', CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	admission := NewSchedulerAdmission(db, SchedulerCapacity{})
	capsule, err := admission.BuildAdmittedTaskCapsule(context.Background(), "run-legacy", "task-legacy", nil)
	if err != nil {
		t.Fatalf("BuildAdmittedTaskCapsule() legacy error: %v", err)
	}
	if len(capsule.Leases) != 0 || len(capsule.DependsOn) != 0 {
		t.Fatalf("legacy capsule unexpectedly owns scheduler state: %+v", capsule)
	}
}

func setupAdmittedCapsuleDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			repository_id TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT,
			status TEXT NOT NULL,
			metadata TEXT DEFAULT '{}',
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
			metadata TEXT DEFAULT '{}',
			updated_at DATETIME
		);
		CREATE TABLE task_specs (
			task_id TEXT PRIMARY KEY,
			files_to_change TEXT DEFAULT '[]',
			files_to_create TEXT DEFAULT '[]'
		);
		CREATE TABLE project_configs (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			test_command TEXT,
			lint_command TEXT,
			typecheck_command TEXT,
			build_command TEXT,
			updated_at DATETIME
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}
