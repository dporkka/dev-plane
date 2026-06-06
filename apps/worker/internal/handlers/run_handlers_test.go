package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/reviewer"
)

func TestScheduleFollowOnRunConsumesHandoffAndQueuesNextRole(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	insertHandoffFixture(t, db, "message-1", "run-1", models.AgentRoleReviewer)

	handler := NewRunHandler(db, slog.Default(), nil)
	scheduled, nextRunID, nextRole, err := handler.scheduleFollowOnRun(context.Background(), events.AgentRunEvent{
		RunID:  "run-1",
		TaskID: "task-1",
	})
	if err != nil {
		t.Fatalf("scheduleFollowOnRun() error: %v", err)
	}
	if !scheduled {
		t.Fatal("scheduled = false, want true")
	}
	if nextRunID == "" {
		t.Fatal("nextRunID is empty")
	}
	if nextRole != models.AgentRoleReviewer {
		t.Fatalf("nextRole = %q, want reviewer", nextRole)
	}

	var role, status, workspaceID, metadata string
	if err := db.QueryRow(`
		SELECT agent_role, status, workspace_id, metadata
		FROM agent_runs
		WHERE id = ?
	`, nextRunID).Scan(&role, &status, &workspaceID, &metadata); err != nil {
		t.Fatalf("query next run: %v", err)
	}
	if role != models.AgentRoleReviewer || status != "queued" || workspaceID != "workspace-1" {
		t.Fatalf("next run = role %q status %q workspace %q", role, status, workspaceID)
	}
	if !contains(metadata, "message-1") || !contains(metadata, "mailbox_handoff") {
		t.Fatalf("metadata = %s, want handoff trace", metadata)
	}

	var consumedBy sql.NullString
	var consumedAt sql.NullString
	if err := db.QueryRow(`
		SELECT consumed_by_run_id, consumed_at
		FROM agent_messages
		WHERE id = 'message-1'
	`).Scan(&consumedBy, &consumedAt); err != nil {
		t.Fatalf("query consumed handoff: %v", err)
	}
	if !consumedBy.Valid || consumedBy.String != nextRunID {
		t.Fatalf("consumed_by_run_id = %v, want %s", consumedBy, nextRunID)
	}
	if !consumedAt.Valid || consumedAt.String == "" {
		t.Fatalf("consumed_at = %v, want timestamp", consumedAt)
	}
}

func TestScheduleFollowOnRunDoesNotDuplicateConsumedHandoff(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	insertHandoffFixture(t, db, "message-1", "run-1", models.AgentRoleReviewer)

	handler := NewRunHandler(db, slog.Default(), nil)
	scheduled, _, _, err := handler.scheduleFollowOnRun(context.Background(), events.AgentRunEvent{RunID: "run-1", TaskID: "task-1"})
	if err != nil || !scheduled {
		t.Fatalf("first schedule = %v, %v", scheduled, err)
	}
	scheduled, _, _, err = handler.scheduleFollowOnRun(context.Background(), events.AgentRunEvent{RunID: "run-1", TaskID: "task-1"})
	if err != nil {
		t.Fatalf("second schedule error: %v", err)
	}
	if scheduled {
		t.Fatal("second schedule = true, want false")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE agent_role = ?`, models.AgentRoleReviewer).Scan(&count); err != nil {
		t.Fatalf("count reviewer runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("reviewer run count = %d, want 1", count)
	}
}

func TestScheduleFollowOnRunRejectsUnknownRole(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	insertHandoffFixture(t, db, "message-1", "run-1", "unknown_role")

	handler := NewRunHandler(db, slog.Default(), nil)
	_, _, _, err := handler.scheduleFollowOnRun(context.Background(), events.AgentRunEvent{RunID: "run-1", TaskID: "task-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "unknown agent role") {
		t.Fatalf("error = %v", err)
	}

	var consumedBy sql.NullString
	if err := db.QueryRow(`SELECT consumed_by_run_id FROM agent_messages WHERE id = 'message-1'`).Scan(&consumedBy); err != nil {
		t.Fatalf("query handoff: %v", err)
	}
	if consumedBy.Valid {
		t.Fatalf("consumed_by_run_id = %q, want null", consumedBy.String)
	}
}

func TestHandleRunCompletedReviewsWhenNoHandoff(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleReviewer)

	reviewService := &fakeReviewer{report: &reviewer.ReviewReport{
		RunID:      "run-1",
		RiskLevel:  "low",
		Approvable: true,
	}}
	handler := NewRunHandler(db, slog.Default(), nil).WithReviewer(reviewService)

	if err := handler.HandleRunCompleted(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)}); err != nil {
		t.Fatalf("HandleRunCompleted() error: %v", err)
	}
	if reviewService.runID != "run-1" {
		t.Fatalf("review runID = %q, want run-1", reviewService.runID)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&status); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if status != "reviewing" {
		t.Fatalf("task status = %q, want reviewing", status)
	}
}

func TestHandleRunCompletedRequiresReviewerWhenNoHandoff(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleReviewer)

	handler := NewRunHandler(db, slog.Default(), nil)
	err := handler.HandleRunCompleted(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "reviewer") {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleRunCompletedReturnsReviewError(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleReviewer)

	handler := NewRunHandler(db, slog.Default(), nil).WithReviewer(&fakeReviewer{err: errors.New("review table missing")})
	err := handler.HandleRunCompleted(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "review table missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleRunTriggeredExecutesQueuedRun(t *testing.T) {
	executor := &fakeRunExecutor{}
	handler := NewRunHandler(nil, slog.Default(), nil).WithRunExecutor(executor)
	msg := &nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1","status":"queued"}`)}

	if err := handler.HandleRunTriggered(msg); err != nil {
		t.Fatalf("HandleRunTriggered() error: %v", err)
	}
	if executor.runID != "run-1" {
		t.Fatalf("executor runID = %q, want run-1", executor.runID)
	}
}

func TestHandleRunTriggeredRequiresExecutor(t *testing.T) {
	handler := NewRunHandler(nil, slog.Default(), nil)
	err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"run-1"}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "executor") {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleRunTriggeredReturnsExecutorError(t *testing.T) {
	executor := &fakeRunExecutor{err: errors.New("model provider unavailable")}
	handler := NewRunHandler(nil, slog.Default(), nil).WithRunExecutor(executor)
	err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"run-1"}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "model provider unavailable") {
		t.Fatalf("error = %v", err)
	}
}

type fakeRunExecutor struct {
	runID string
	err   error
}

func (e *fakeRunExecutor) ExecuteRun(ctx context.Context, runID string) error {
	e.runID = runID
	return e.err
}

type fakeReviewer struct {
	runID  string
	report *reviewer.ReviewReport
	err    error
}

func (r *fakeReviewer) Review(ctx context.Context, runID string) (*reviewer.ReviewReport, error) {
	r.runID = runID
	if r.err != nil {
		return nil, r.err
	}
	if r.report != nil {
		return r.report, nil
	}
	return &reviewer.ReviewReport{RunID: runID, RiskLevel: "low", Approvable: true}, nil
}

func setupRunHandlerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			deleted_at DATETIME,
			updated_at DATETIME
		);
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT,
			agent_role TEXT NOT NULL,
			model TEXT,
			provider TEXT,
			status TEXT NOT NULL,
			total_cost REAL DEFAULT 0,
			metadata TEXT DEFAULT '{}',
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE TABLE agent_messages (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_run_id TEXT,
			from_agent TEXT NOT NULL,
			to_agent TEXT NOT NULL,
			message_type TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT DEFAULT '{}',
			consumed_at DATETIME,
			consumed_by_run_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func insertCompletedRunFixture(t *testing.T, db *sql.DB, role string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO tasks (id, status) VALUES ('task-1', 'running');
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, total_cost, metadata
		) VALUES (
			'run-1', 'task-1', 'workspace-1', ?, 'gpt-4o', 'openai', 'completed', 0, '{}'
		);
	`, role)
	if err != nil {
		t.Fatalf("insert completed run fixture: %v", err)
	}
}

func insertHandoffFixture(t *testing.T, db *sql.DB, id, runID, toAgent string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO agent_messages (
			id, task_id, agent_run_id, from_agent, to_agent, message_type, content, metadata
		) VALUES (
			?, 'task-1', ?, 'implementer', ?, 'handoff', 'review this change', '{}'
		)
	`, id, runID, toAgent)
	if err != nil {
		t.Fatalf("insert handoff fixture: %v", err)
	}
}

func contains(value, substr string) bool {
	return strings.Contains(value, substr)
}
