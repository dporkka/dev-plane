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
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			metadata TEXT
		);
		INSERT INTO agent_runs (id, metadata) VALUES ('run-1', ?);
	`, metadata); err != nil {
		_ = db.Close()
		t.Fatalf("create dispatch fixture: %v", err)
	}
	return db
}
