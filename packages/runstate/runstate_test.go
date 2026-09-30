package runstate

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/models"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL,
			state_version INTEGER NOT NULL DEFAULT 1,
			processed_event_version INTEGER NOT NULL DEFAULT 0,
			processing_event_version INTEGER NOT NULL DEFAULT 0,
			event_claimed_at DATETIME,
			outcome TEXT,
			error_message TEXT,
			summary TEXT,
			total_cost REAL DEFAULT 0,
			started_at DATETIME,
			completed_at DATETIME,
			updated_at DATETIME
		);
	`); err != nil {
		db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func TestTransitionIncrementsVersionAndRejectsTerminalRegression(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO agent_runs (id, task_id, status) VALUES ('run-1', 'task-1', 'queued')`); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	result, err := Transition(context.Background(), db, Request{
		RunID:     "run-1",
		ToStatus:  models.AgentRunStatusRunning,
		StartedAt: &now,
	})
	if err != nil {
		t.Fatalf("Transition queued->running: %v", err)
	}
	if result.StateVersion != 2 || result.PreviousStatus != models.AgentRunStatusQueued || result.Status != models.AgentRunStatusRunning {
		t.Fatalf("transition result = %#v", result)
	}

	completed := now.Add(time.Minute)
	result, err = Transition(context.Background(), db, Request{
		RunID:       "run-1",
		ToStatus:    models.AgentRunStatusCompleted,
		CompletedAt: &completed,
	})
	if err != nil {
		t.Fatalf("Transition running->completed: %v", err)
	}
	if result.StateVersion != 3 {
		t.Fatalf("completed version = %d, want 3", result.StateVersion)
	}

	_, err = Transition(context.Background(), db, Request{
		RunID:    "run-1",
		ToStatus: models.AgentRunStatusRunning,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("completed->running error = %v, want ErrInvalidTransition", err)
	}

	var status string
	var version int64
	if err := db.QueryRow(`SELECT status, state_version FROM agent_runs WHERE id = 'run-1'`).Scan(&status, &version); err != nil {
		t.Fatal(err)
	}
	if status != models.AgentRunStatusCompleted || version != 3 {
		t.Fatalf("persisted state = %s v%d, want completed v3", status, version)
	}
}

func TestLifecycleClaimSuppressesDuplicateStaleAndSupersededEvents(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`
		INSERT INTO agent_runs (id, task_id, attempt, status, state_version)
		VALUES ('run-1', 'task-1', 1, 'completed', 3)
	`); err != nil {
		t.Fatal(err)
	}

	claim, err := ClaimLifecycleEvent(context.Background(), db, LifecycleEvent{
		RunID:        "run-1",
		Status:       models.AgentRunStatusCompleted,
		StateVersion: 3,
	})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !claim.Claimed || claim.StateVersion != 3 {
		t.Fatalf("first claim = %#v", claim)
	}
	if err := CompleteLifecycleEvent(context.Background(), db, claim); err != nil {
		t.Fatalf("complete claim: %v", err)
	}

	duplicate, err := ClaimLifecycleEvent(context.Background(), db, LifecycleEvent{
		RunID:        "run-1",
		Status:       models.AgentRunStatusCompleted,
		StateVersion: 3,
	})
	if err != nil {
		t.Fatalf("duplicate claim: %v", err)
	}
	if duplicate.Claimed {
		t.Fatal("duplicate terminal event was claimed")
	}

	stale, err := ClaimLifecycleEvent(context.Background(), db, LifecycleEvent{
		RunID:        "run-1",
		Status:       models.AgentRunStatusCompleted,
		StateVersion: 2,
	})
	if err != nil {
		t.Fatalf("stale claim: %v", err)
	}
	if stale.Claimed {
		t.Fatal("stale state version was claimed")
	}

	if _, err := db.Exec(`
		INSERT INTO agent_runs (id, task_id, attempt, status, state_version)
		VALUES ('run-2', 'task-1', 2, 'queued', 1)
	`); err != nil {
		t.Fatal(err)
	}
	superseded, err := ClaimLifecycleEvent(context.Background(), db, LifecycleEvent{
		RunID:        "run-1",
		Status:       models.AgentRunStatusCompleted,
		StateVersion: 3,
		RequireLatestAttempt: true,
	})
	if err != nil {
		t.Fatalf("superseded claim: %v", err)
	}
	if superseded.Claimed {
		t.Fatal("superseded run event was claimed")
	}
}
