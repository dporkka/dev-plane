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
)

func TestHandleApprovalApprovedCreatesPROnPRCreateApproval(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "reviewing")

	creator := &fakePullRequestCreator{
		pr: &models.PullRequest{ID: "pr-1", TaskID: "task-1", Number: 42},
	}
	handler := NewApprovalHandler(db, slog.Default(), nil).WithPullRequestCreator(creator)

	err := handler.HandleApprovalApproved(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"response":"approved",
		"approval_type":"pr_create"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalApproved() error: %v", err)
	}
	if creator.taskID != "task-1" {
		t.Fatalf("creator taskID = %q, want task-1", creator.taskID)
	}
}

func TestHandleApprovalApprovedIgnoresNonPRApproval(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "reviewing")

	creator := &fakePullRequestCreator{
		pr: &models.PullRequest{ID: "pr-1", TaskID: "task-1", Number: 42},
	}
	handler := NewApprovalHandler(db, slog.Default(), nil).WithPullRequestCreator(creator)

	err := handler.HandleApprovalApproved(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"response":"approved",
		"approval_type":"execution"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalApproved() error: %v", err)
	}
	if creator.taskID != "" {
		t.Fatalf("creator taskID = %q, want no call", creator.taskID)
	}
}

func TestHandleApprovalApprovedResumesPausedRiskyActionRun(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "running")
	insertApprovalRunFixture(t, db, "run-1", "task-1", models.AgentRunStatusPaused)
	publisher := &fakeWorkerEventPublisher{}
	handler := NewApprovalHandler(db, slog.Default(), nil).WithEventPublisher(publisher)

	err := handler.HandleApprovalApproved(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"agent_run_id":"run-1",
		"response":"approved",
		"approval_type":"risky_action"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalApproved() error: %v", err)
	}

	var runStatus string
	var errorMessage sql.NullString
	if err := db.QueryRow(`SELECT status, error_message FROM agent_runs WHERE id = 'run-1'`).Scan(&runStatus, &errorMessage); err != nil {
		t.Fatalf("query run: %v", err)
	}
	if runStatus != models.AgentRunStatusQueued {
		t.Fatalf("run status = %q, want queued", runStatus)
	}
	if errorMessage.Valid {
		t.Fatalf("error_message = %q, want null", errorMessage.String)
	}
	if publisher.subject != events.RunTriggered {
		t.Fatalf("published subject = %q, want %s", publisher.subject, events.RunTriggered)
	}
	if !strings.Contains(string(publisher.data), `"approval_resumed"`) || !strings.Contains(string(publisher.data), `"run-1"`) {
		t.Fatalf("published data = %s", string(publisher.data))
	}
}

func TestHandleApprovalApprovedResumesPausedCapabilityRunFromApprovalRecord(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "running")
	insertApprovalRunFixture(t, db, "run-1", "task-1", models.AgentRunStatusPaused)
	insertApprovalFixture(t, db, "approval-1", "task-1", "run-1", "capability:write_file")
	publisher := &fakeWorkerEventPublisher{}
	handler := NewApprovalHandler(db, slog.Default(), nil).WithEventPublisher(publisher)

	err := handler.HandleApprovalApproved(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"response":"approved",
		"approval_type":"capability:write_file"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalApproved() error: %v", err)
	}

	var runStatus string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-1'`).Scan(&runStatus); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if runStatus != models.AgentRunStatusQueued {
		t.Fatalf("run status = %q, want queued", runStatus)
	}
	if publisher.subject != events.RunTriggered {
		t.Fatalf("published subject = %q, want %s", publisher.subject, events.RunTriggered)
	}
}

func TestHandleApprovalApprovedSkipsNonReviewableTask(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "running")

	creator := &fakePullRequestCreator{
		pr: &models.PullRequest{ID: "pr-1", TaskID: "task-1", Number: 42},
	}
	handler := NewApprovalHandler(db, slog.Default(), nil).WithPullRequestCreator(creator)

	err := handler.HandleApprovalApproved(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"response":"approved",
		"approval_type":"pr_create"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalApproved() error: %v", err)
	}
	if creator.taskID != "" {
		t.Fatalf("creator taskID = %q, want no call", creator.taskID)
	}
}

func TestHandleApprovalApprovedReturnsPRCreationError(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "reviewing")

	handler := NewApprovalHandler(db, slog.Default(), nil).WithPullRequestCreator(&fakePullRequestCreator{
		err: errors.New("github token is not configured"),
	})

	err := handler.HandleApprovalApproved(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"response":"approved",
		"approval_type":"pr_create"
	}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "github token is not configured") {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleApprovalRejectedFailsTaskAndCompletedRun(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "reviewing")
	_, err := db.Exec(`
		INSERT INTO agent_runs (id, task_id, status, error_message, updated_at)
		VALUES ('run-1', 'task-1', 'completed', NULL, NULL)
	`)
	if err != nil {
		t.Fatalf("insert run fixture: %v", err)
	}

	handler := NewApprovalHandler(db, slog.Default(), nil)
	err = handler.HandleApprovalRejected(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"response":"rejected",
		"responder_id":"user-1",
		"note":"needs changes"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalRejected() error: %v", err)
	}

	var taskStatus string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if taskStatus != "failed" {
		t.Fatalf("task status = %q, want failed", taskStatus)
	}

	var runStatus, errorMessage string
	if err := db.QueryRow(`SELECT status, error_message FROM agent_runs WHERE id = 'run-1'`).Scan(&runStatus, &errorMessage); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if runStatus != "failed" {
		t.Fatalf("run status = %q, want failed", runStatus)
	}
	if !strings.Contains(errorMessage, "needs changes") {
		t.Fatalf("error_message = %q, want rejection note", errorMessage)
	}
}

func TestHandleApprovalRejectedFailsPausedRunByID(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "running")
	insertApprovalRunFixture(t, db, "run-1", "task-1", models.AgentRunStatusPaused)

	handler := NewApprovalHandler(db, slog.Default(), nil)
	err := handler.HandleApprovalRejected(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"agent_run_id":"run-1",
		"response":"rejected",
		"responder_id":"user-1",
		"note":"too risky"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalRejected() error: %v", err)
	}

	var taskStatus, runStatus, errorMessage string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if err := db.QueryRow(`SELECT status, error_message FROM agent_runs WHERE id = 'run-1'`).Scan(&runStatus, &errorMessage); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if taskStatus != "failed" || runStatus != models.AgentRunStatusFailed {
		t.Fatalf("task/run status = %q/%q, want failed/failed", taskStatus, runStatus)
	}
	if !strings.Contains(errorMessage, "too risky") {
		t.Fatalf("error_message = %q, want rejection note", errorMessage)
	}
}

type fakeWorkerEventPublisher struct {
	subject string
	data    []byte
	err     error
}

func (p *fakeWorkerEventPublisher) Publish(subject string, data []byte) error {
	p.subject = subject
	p.data = append([]byte(nil), data...)
	return p.err
}

type fakePullRequestCreator struct {
	taskID string
	pr     *models.PullRequest
	err    error
}

func (f *fakePullRequestCreator) CreatePullRequest(ctx context.Context, taskID string) (*models.PullRequest, error) {
	f.taskID = taskID
	if f.err != nil {
		return nil, f.err
	}
	if f.pr != nil {
		return f.pr, nil
	}
	return &models.PullRequest{ID: "pr-1", TaskID: taskID, Number: 1}, nil
}

func setupApprovalHandlerDB(t *testing.T) *sql.DB {
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
			status TEXT NOT NULL,
			error_message TEXT,
			updated_at DATETIME
		);
		CREATE TABLE approvals (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_run_id TEXT,
			approval_type TEXT NOT NULL
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func insertApprovalTaskFixture(t *testing.T, db *sql.DB, id, status string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tasks (id, status) VALUES (?, ?)`, id, status); err != nil {
		t.Fatalf("insert task fixture: %v", err)
	}
}

func insertApprovalRunFixture(t *testing.T, db *sql.DB, id, taskID, status string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO agent_runs (id, task_id, status, error_message) VALUES (?, ?, ?, 'paused for approval')`, id, taskID, status); err != nil {
		t.Fatalf("insert run fixture: %v", err)
	}
}

func insertApprovalFixture(t *testing.T, db *sql.DB, id, taskID, runID, approvalType string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO approvals (id, task_id, agent_run_id, approval_type) VALUES (?, ?, ?, ?)`, id, taskID, runID, approvalType); err != nil {
		t.Fatalf("insert approval fixture: %v", err)
	}
}
