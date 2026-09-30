package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/scheduler"
)

type capsuleAssertingExecutor struct {
	db *sql.DB
	runID string
	sawCapsule bool
}

func (e *capsuleAssertingExecutor) ExecuteRun(_ context.Context, runID string) error {
	e.runID = runID
	var payload string
	if err := e.db.QueryRow(`
		SELECT payload FROM task_capsules WHERE agent_run_id = ?
	`, runID).Scan(&payload); err != nil {
		return fmt.Errorf("capsule missing before executor dispatch: %w", err)
	}

	var capsule scheduler.TaskCapsule
	if err := json.Unmarshal([]byte(payload), &capsule); err != nil {
		return fmt.Errorf("decode persisted capsule: %w", err)
	}
	if capsule.TaskID != "task-1" || capsule.WorkspaceID != "workspace-1" {
		return fmt.Errorf("unexpected persisted capsule identity: %+v", capsule)
	}
	if len(capsule.Leases) != 1 || capsule.Leases[0].Path != "apps/api" {
		return fmt.Errorf("unexpected persisted leases: %+v", capsule.Leases)
	}
	e.sawCapsule = true
	return nil
}

func TestHandleRunTriggeredPersistsAdmittedCapsuleBeforeExecutorDispatch(t *testing.T) {
	db := setupAdmittedCapsuleDB(t)
	defer db.Close()

	_, err := db.Exec(`
		CREATE TABLE task_capsules (
			agent_run_id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			agent_id TEXT NOT NULL,
			agent_role TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE task_leases (
			agent_run_id TEXT NOT NULL,
			path TEXT NOT NULL,
			mode TEXT NOT NULL,
			acquired_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (agent_run_id, path)
		);
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
			'queued',
			'{"admission":{"policy":"task-readiness-v1","readiness":{"status":"ready","checks":[]}}}',
			CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	executor := &capsuleAssertingExecutor{db: db}
	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 2, CPU: 4, MemoryMB: 4096})
	handler := NewRunHandler(db, slog.Default(), nil).
		WithRunExecutor(executor).
		WithRunAdmission(admission)

	err = handler.HandleRunTriggered(&nats.Msg{
		Data: []byte(`{"run_id":"run-1","task_id":"task-1","status":"queued"}`),
	})
	if err != nil {
		t.Fatalf("HandleRunTriggered() error: %v", err)
	}
	if executor.runID != "run-1" || !executor.sawCapsule {
		t.Fatalf("executor state = run %q capsule=%v", executor.runID, executor.sawCapsule)
	}
}
