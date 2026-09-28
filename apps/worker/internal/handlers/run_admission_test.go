package handlers

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/scheduler"
)

type fakeStartBudget struct {
	allowed bool
	reason  string
	err     error
	runID   string
}

func (b *fakeStartBudget) CheckRunStart(ctx context.Context, runID string) (bool, string, error) {
	b.runID = runID
	return b.allowed, b.reason, b.err
}

func TestSchedulerAdmissionAllowsDisjointOwnershipWithinCapacity(t *testing.T) {
	db := setupAdmissionDB(t)
	defer db.Close()
	seedAdmissionTask(t, db, "task-running", "project-1", "repo-1", "running", `{"scheduler":{"owns":["apps/api"],"cpu":2,"memory_mb":1024}}`)
	seedAdmissionRun(t, db, "run-running", "task-running", "running")
	seedAdmissionTask(t, db, "task-queued", "project-1", "repo-1", "running", `{"scheduler":{"owns":["apps/web"],"cpu":2,"memory_mb":1024}}`)
	seedAdmissionRun(t, db, "run-queued", "task-queued", "queued")

	budget := &fakeStartBudget{allowed: true}
	admission := NewSchedulerAdmission(db, budget, SchedulerAdmissionConfig{
		MaxParallel: 3,
		Capacity: scheduler.Capacity{CPU: 4, MemoryMB: 4096},
	})
	decision, err := admission.CheckRunAdmission(context.Background(), "run-queued", "task-queued")
	if err != nil {
		t.Fatalf("CheckRunAdmission() error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("decision = %+v, want allowed", decision)
	}
	if budget.runID != "run-queued" {
		t.Fatalf("budget runID = %q", budget.runID)
	}
}

func TestSchedulerAdmissionBlocksOwnershipConflict(t *testing.T) {
	db := setupAdmissionDB(t)
	defer db.Close()
	seedAdmissionTask(t, db, "task-running", "project-1", "repo-1", "running", `{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}`)
	seedAdmissionRun(t, db, "run-running", "task-running", "running")
	seedAdmissionTask(t, db, "task-queued", "project-1", "repo-1", "running", `{"scheduler":{"owns":["apps/api/routes"],"cpu":1,"memory_mb":512}}`)
	seedAdmissionRun(t, db, "run-queued", "task-queued", "queued")

	admission := NewSchedulerAdmission(db, &fakeStartBudget{allowed: true}, SchedulerAdmissionConfig{
		MaxParallel: 3,
		Capacity: scheduler.Capacity{CPU: 4, MemoryMB: 4096},
	})
	decision, err := admission.CheckRunAdmission(context.Background(), "run-queued", "task-queued")
	if err != nil {
		t.Fatalf("CheckRunAdmission() error: %v", err)
	}
	if decision.Allowed || !strings.Contains(decision.Reason, "scheduler-blocked") {
		t.Fatalf("decision = %+v, want scheduler blocked", decision)
	}
}

func TestSchedulerAdmissionBlocksWhenRunningResourcesConsumeCapacity(t *testing.T) {
	db := setupAdmissionDB(t)
	defer db.Close()
	seedAdmissionTask(t, db, "task-running", "project-1", "repo-1", "running", `{"scheduler":{"owns":["apps/api"],"cpu":3,"memory_mb":3072}}`)
	seedAdmissionRun(t, db, "run-running", "task-running", "running")
	seedAdmissionTask(t, db, "task-queued", "project-1", "repo-1", "running", `{"scheduler":{"owns":["apps/web"],"cpu":2,"memory_mb":2048}}`)
	seedAdmissionRun(t, db, "run-queued", "task-queued", "queued")

	admission := NewSchedulerAdmission(db, &fakeStartBudget{allowed: true}, SchedulerAdmissionConfig{
		MaxParallel: 3,
		Capacity: scheduler.Capacity{CPU: 4, MemoryMB: 4096},
	})
	decision, err := admission.CheckRunAdmission(context.Background(), "run-queued", "task-queued")
	if err != nil {
		t.Fatalf("CheckRunAdmission() error: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("decision = %+v, want capacity block", decision)
	}
}

func TestSchedulerAdmissionFallsBackToWholeRepositoryOwnership(t *testing.T) {
	db := setupAdmissionDB(t)
	defer db.Close()
	seedAdmissionTask(t, db, "task-running", "project-1", "repo-1", "running", `{}`)
	seedAdmissionRun(t, db, "run-running", "task-running", "running")
	seedAdmissionTask(t, db, "task-queued", "project-1", "repo-1", "running", `{}`)
	seedAdmissionRun(t, db, "run-queued", "task-queued", "queued")

	admission := NewSchedulerAdmission(db, &fakeStartBudget{allowed: true}, SchedulerAdmissionConfig{
		MaxParallel: 3,
		Capacity: scheduler.Capacity{CPU: 4, MemoryMB: 4096},
	})
	decision, err := admission.CheckRunAdmission(context.Background(), "run-queued", "task-queued")
	if err != nil {
		t.Fatalf("CheckRunAdmission() error: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("decision = %+v, want conservative repository conflict", decision)
	}
}

func TestSchedulerAdmissionStopsAtBudgetDenial(t *testing.T) {
	db := setupAdmissionDB(t)
	defer db.Close()
	seedAdmissionTask(t, db, "task-queued", "project-1", "repo-1", "running", `{"scheduler":{"owns":["apps/web"],"cpu":1,"memory_mb":512}}`)
	seedAdmissionRun(t, db, "run-queued", "task-queued", "queued")

	admission := NewSchedulerAdmission(db, &fakeStartBudget{allowed: false, reason: "concurrent runs 2 reached max 2"}, SchedulerAdmissionConfig{
		MaxParallel: 3,
		Capacity: scheduler.Capacity{CPU: 4, MemoryMB: 4096},
	})
	decision, err := admission.CheckRunAdmission(context.Background(), "run-queued", "task-queued")
	if err != nil {
		t.Fatalf("CheckRunAdmission() error: %v", err)
	}
	if decision.Allowed || !strings.Contains(decision.Reason, "budget") {
		t.Fatalf("decision = %+v, want budget block", decision)
	}
}

func TestSchedulerAdmissionFailsClosedOnBudgetError(t *testing.T) {
	db := setupAdmissionDB(t)
	defer db.Close()
	admission := NewSchedulerAdmission(db, &fakeStartBudget{err: errors.New("budget db unavailable")}, SchedulerAdmissionConfig{
		MaxParallel: 3,
		Capacity: scheduler.Capacity{CPU: 4, MemoryMB: 4096},
	})
	_, err := admission.CheckRunAdmission(context.Background(), "run-queued", "task-queued")
	if err == nil || !strings.Contains(err.Error(), "budget db unavailable") {
		t.Fatalf("error = %v, want budget failure", err)
	}
}

func setupAdmissionDB(t *testing.T) *sql.DB {
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
			status TEXT NOT NULL,
			metadata TEXT DEFAULT '{}'
		);
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			status TEXT NOT NULL
		);
	`)
	if err != nil {
		db.Close()
		t.Fatalf("create admission schema: %v", err)
	}
	return db
}

func seedAdmissionTask(t *testing.T, db *sql.DB, id, projectID, repositoryID, status, metadata string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tasks (id, project_id, repository_id, status, metadata) VALUES (?, ?, ?, ?, ?)`,
		id, projectID, repositoryID, status, metadata); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func seedAdmissionRun(t *testing.T, db *sql.DB, id, taskID, status string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO agent_runs (id, task_id, status) VALUES (?, ?, ?)`, id, taskID, status); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}
