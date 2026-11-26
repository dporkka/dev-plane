package agentexecutor

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

type fakeLegacyRunBackend struct {
	runID string
	err   error
}

func (f *fakeLegacyRunBackend) Run(_ context.Context, runID string) error {
	f.runID = runID
	return f.err
}

type fakeExternalRunBackend struct {
	runID string
	err   error
}

func (f *fakeExternalRunBackend) ExecuteExternalRun(_ context.Context, runID string) error {
	f.runID = runID
	return f.err
}

func TestExecuteRunDefaultsToLegacyBackend(t *testing.T) {
	db := setupDispatchDB(t, "{}")
	defer db.Close()

	legacy := &fakeLegacyRunBackend{}
	external := &fakeExternalRunBackend{}
	executor := &Executor{db: db, legacy: legacy, external: external}

	if err := executor.ExecuteRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("ExecuteRun() error = %v", err)
	}
	if legacy.runID != "run-1" {
		t.Fatalf("legacy run id = %q, want run-1", legacy.runID)
	}
	if external.runID != "" {
		t.Fatalf("external run id = %q, want no call", external.runID)
	}
}

func TestExecuteRunDispatchesExplicitAgentRuntimeBackend(t *testing.T) {
	db := setupDispatchDB(t, `{"execution":{"backend":"agent_runtime","provider":"codex"}}`)
	defer db.Close()

	legacy := &fakeLegacyRunBackend{}
	external := &fakeExternalRunBackend{}
	executor := &Executor{db: db, legacy: legacy, external: external}

	if err := executor.ExecuteRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("ExecuteRun() error = %v", err)
	}
	if external.runID != "run-1" {
		t.Fatalf("external run id = %q, want run-1", external.runID)
	}
	if legacy.runID != "" {
		t.Fatalf("legacy run id = %q, want no call", legacy.runID)
	}
}

func TestExecuteRunFailsClosedForUnknownBackend(t *testing.T) {
	db := setupDispatchDB(t, `{"execution":{"backend":"mystery"}}`)
	defer db.Close()

	executor := &Executor{db: db, legacy: &fakeLegacyRunBackend{}, external: &fakeExternalRunBackend{}}
	err := executor.ExecuteRun(context.Background(), "run-1")
	if err == nil || !strings.Contains(err.Error(), "unsupported execution backend") {
		t.Fatalf("ExecuteRun() error = %v, want unsupported backend error", err)
	}
}

func TestExecuteRunFailsClosedWhenAgentRuntimeBackendIsUnavailable(t *testing.T) {
	db := setupDispatchDB(t, `{"execution":{"backend":"agent_runtime"}}`)
	defer db.Close()

	executor := &Executor{db: db, legacy: &fakeLegacyRunBackend{}}
	err := executor.ExecuteRun(context.Background(), "run-1")
	if err == nil || !strings.Contains(err.Error(), "agent runtime executor is not configured") {
		t.Fatalf("ExecuteRun() error = %v, want configuration error", err)
	}
}

func TestExecuteRunRejectsMalformedMetadata(t *testing.T) {
	db := setupDispatchDB(t, `{"execution":`)
	defer db.Close()

	executor := &Executor{db: db, legacy: &fakeLegacyRunBackend{}}
	err := executor.ExecuteRun(context.Background(), "run-1")
	if err == nil || !strings.Contains(err.Error(), "decode execution metadata") {
		t.Fatalf("ExecuteRun() error = %v, want metadata error", err)
	}
}

func setupDispatchDB(t *testing.T, metadata string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
        CREATE TABLE organizations(id TEXT PRIMARY KEY,deleted_at TEXT);
        CREATE TABLE projects(id TEXT PRIMARY KEY,organization_id TEXT,deleted_at TEXT);
        CREATE TABLE repositories(id TEXT PRIMARY KEY,project_id TEXT,settings TEXT,owner TEXT,name TEXT,full_name TEXT,clone_url TEXT,deleted_at TEXT);
        CREATE TABLE tasks(id TEXT PRIMARY KEY,project_id TEXT,repository_id TEXT,workspace_id TEXT,deleted_at TEXT);
        CREATE TABLE workspaces(id TEXT PRIMARY KEY,task_id TEXT,repository_id TEXT,runtime_provider TEXT,runtime_session_id TEXT,worktree_path TEXT,status TEXT,deleted_at TEXT);
        CREATE TABLE gitea_repository_bindings(repository_id TEXT,project_id TEXT,organization_id TEXT,instance_url TEXT,upstream_repository_id INTEGER,full_name TEXT,authorization_generation INTEGER DEFAULT 1,revoked_at TEXT);
        CREATE TABLE agent_runs (id TEXT PRIMARY KEY,task_id TEXT,workspace_id TEXT,metadata TEXT);
        INSERT INTO organizations(id) VALUES ('org-1');
        INSERT INTO projects(id,organization_id) VALUES('project-1','org-1');
        INSERT INTO repositories(id,project_id) VALUES('repo-1','project-1');
        INSERT INTO tasks(id,project_id,repository_id) VALUES('task-1','project-1','repo-1');
        INSERT INTO agent_runs (id,task_id,metadata) VALUES ('run-1','task-1',?);
	`, metadata); err != nil {
		_ = db.Close()
		t.Fatalf("create dispatch fixture: %v", err)
	}
	return db
}
