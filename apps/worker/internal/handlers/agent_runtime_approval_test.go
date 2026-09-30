package handlers

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/models"
)

type fakeAgentRuntimeApprovalResponder struct {
	threadID string
	turnID   string
	itemID   string
	approved bool
	note     string
}

func (f *fakeAgentRuntimeApprovalResponder) RespondAgentApproval(_ context.Context, threadID, turnID, itemID string, approved bool, note string) error {
	f.threadID = threadID
	f.turnID = turnID
	f.itemID = itemID
	f.approved = approved
	f.note = note
	return nil
}

func TestHandleAgentRuntimeApprovalApprovedRespondsProviderWithoutRequeue(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	addApprovalMetadataColumn(t, db)
	insertApprovalTaskFixture(t, db, "task-1", "running")
	insertApprovalRunFixture(t, db, "run-1", "task-1", models.AgentRunStatusPaused)
	insertRuntimeApprovalFixture(t, db, "approval-1", "task-1", "run-1")

	responder := &fakeAgentRuntimeApprovalResponder{}
	handler := NewApprovalHandler(db, slog.Default(), nil).WithAgentRuntimeApprovalResponder(responder)

	err := handler.HandleApprovalApproved(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"agent_run_id":"run-1",
		"response":"approved",
		"approval_type":"agent_runtime",
		"note":"ship it"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalApproved() error = %v", err)
	}
	if responder.threadID != "thread-1" || responder.turnID != "turn-1" || responder.itemID != "item-1" {
		t.Fatalf("responder ids = %q/%q/%q", responder.threadID, responder.turnID, responder.itemID)
	}
	if !responder.approved || responder.note != "ship it" {
		t.Fatalf("responder decision = approved:%v note:%q", responder.approved, responder.note)
	}

	var runStatus string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-1'`).Scan(&runStatus); err != nil {
		t.Fatalf("query run: %v", err)
	}
	if runStatus != models.AgentRunStatusPaused {
		t.Fatalf("run status = %q, want paused; provider turn is resumed directly", runStatus)
	}
}

func TestHandleAgentRuntimeApprovalRejectedDeniesSingleActionWithoutFailingTask(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	addApprovalMetadataColumn(t, db)
	insertApprovalTaskFixture(t, db, "task-1", "running")
	insertApprovalRunFixture(t, db, "run-1", "task-1", models.AgentRunStatusPaused)
	insertRuntimeApprovalFixture(t, db, "approval-1", "task-1", "run-1")

	responder := &fakeAgentRuntimeApprovalResponder{}
	handler := NewApprovalHandler(db, slog.Default(), nil).WithAgentRuntimeApprovalResponder(responder)

	err := handler.HandleApprovalRejected(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"agent_run_id":"run-1",
		"response":"rejected",
		"approval_type":"agent_runtime",
		"responder_id":"user-1",
		"note":"do not run this command"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalRejected() error = %v", err)
	}
	if responder.approved {
		t.Fatal("responder approved = true, want false")
	}

	var taskStatus, runStatus string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus); err != nil {
		t.Fatalf("query task: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-1'`).Scan(&runStatus); err != nil {
		t.Fatalf("query run: %v", err)
	}
	if taskStatus != "running" || runStatus != models.AgentRunStatusPaused {
		t.Fatalf("task/run status = %q/%q, want running/paused", taskStatus, runStatus)
	}
}

func addApprovalMetadataColumn(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`ALTER TABLE approvals ADD COLUMN metadata TEXT`); err != nil {
		t.Fatalf("add approval metadata column: %v", err)
	}
}

func insertRuntimeApprovalFixture(t *testing.T, db *sql.DB, id, taskID, runID string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO approvals (id, task_id, agent_run_id, approval_type, metadata) VALUES (?, ?, ?, ?, ?)`,
		id, taskID, runID, models.ApprovalTypeAgentRuntime,
		`{"thread_id":"thread-1","turn_id":"turn-1","item_id":"item-1"}`,
	); err != nil {
		t.Fatalf("insert runtime approval fixture: %v", err)
	}
}
