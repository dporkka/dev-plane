package handlers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"

	_ "github.com/mattn/go-sqlite3"
)

func reviewApprovalGateDB(t *testing.T) *sql.DB {
	t.Helper()
	db := setupRunHandlerDB(t)
	t.Cleanup(func() { _ = db.Close() })
	insertCompletedRunFixture(t, db, "implementer")
	_, err := db.Exec(`
		UPDATE tasks SET status = 'reviewing' WHERE id = 'task-1';
		CREATE TABLE review_reports (
			run_id TEXT PRIMARY KEY,
			approvable BOOLEAN NOT NULL,
			risk_level TEXT NOT NULL
		);
		CREATE TABLE approvals (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_run_id TEXT,
			approval_type TEXT,
			requested_by TEXT,
			requested_at DATETIME,
			metadata TEXT,
			created_at DATETIME,
			updated_at DATETIME,
			response TEXT,
			expires_at DATETIME
		);
	`)
	if err != nil {
		t.Fatalf("prepare approval gate schema: %v", err)
	}
	return db
}

func reviewApprovalEvent(t *testing.T, runID, taskID string) *nats.Msg {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"run_id": runID, "task_id": taskID, "status": "completed",
		"risk_level": "low", "approvable": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &nats.Msg{Data: data}
}

func approvalCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM approvals").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestReviewCompletedAuthorizesFromPersistedReviewNotEvent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		persisted    bool
		approvable   bool
		runStatus    string
		taskStatus   string
		eventTask    string
		expectCount  int
	}{
		{name: "persisted approvable review", persisted: true, approvable: true, runStatus: "completed", taskStatus: "reviewing", eventTask: "task-1", expectCount: 1},
		{name: "legacy API event omits task id", persisted: true, approvable: true, runStatus: "completed", taskStatus: "reviewing", eventTask: "", expectCount: 1},
		{name: "forged positive event over non approvable review", persisted: true, approvable: false, runStatus: "completed", taskStatus: "reviewing", eventTask: "task-1"},
		{name: "review missing from database", persisted: false, runStatus: "completed", taskStatus: "reviewing", eventTask: "task-1"},
		{name: "run is not completed", persisted: true, approvable: true, runStatus: "failed", taskStatus: "reviewing", eventTask: "task-1"},
		{name: "task not in reviewing state", persisted: true, approvable: true, runStatus: "completed", taskStatus: "running", eventTask: "task-1"},
		{name: "spoofed task association", persisted: true, approvable: true, runStatus: "completed", taskStatus: "reviewing", eventTask: "another-task"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := reviewApprovalGateDB(t)
			if tc.persisted {
				_, err := db.Exec("INSERT INTO review_reports (run_id, approvable, risk_level) VALUES (?, ?, 'low')", "run-1", tc.approvable)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec("UPDATE agent_runs SET status = ? WHERE id = 'run-1'", tc.runStatus); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE tasks SET status = ? WHERE id = 'task-1'", tc.taskStatus); err != nil {
				t.Fatal(err)
			}
			h := NewRunHandler(db, slog.Default(), nil)
			_ = h.HandleReviewCompleted(reviewApprovalEvent(t, "run-1", tc.eventTask))
			if got := approvalCount(t, db); got != tc.expectCount {
				t.Fatalf("PR creation approvals = %d, want %d", got, tc.expectCount)
			}
			if tc.expectCount == 1 {
				var task, run string
				if err := db.QueryRow("SELECT task_id, agent_run_id FROM approvals LIMIT 1").Scan(&task, &run); err != nil {
					t.Fatal(err)
				}
				if task != "task-1" || run != "run-1" {
					t.Fatalf("approval bound to task=%q run=%q", task, run)
				}
			}
		})
	}
}

func TestReviewCompletedDoesNotDuplicatePendingApproval(t *testing.T) {
	db := reviewApprovalGateDB(t)
	if _, err := db.Exec("INSERT INTO review_reports (run_id, approvable, risk_level) VALUES ('run-1', 1, 'low')"); err != nil {
		t.Fatal(err)
	}
	h := NewRunHandler(db, slog.Default(), nil)
	for i := 0; i < 2; i++ {
		if err := h.HandleReviewCompleted(reviewApprovalEvent(t, "run-1", "task-1")); err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}
	if got := approvalCount(t, db); got != 1 {
		t.Fatalf("duplicate delivery created %d approvals, want 1", got)
	}
}
