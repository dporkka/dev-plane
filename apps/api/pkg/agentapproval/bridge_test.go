package agentapproval

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
)

type fakePublisher struct {
	subject string
	data    []byte
	count   int
}

func (p *fakePublisher) Publish(subject string, data []byte) error {
	p.subject = subject
	p.data = append([]byte(nil), data...)
	p.count++
	return nil
}

func TestBridgeCreatesIdempotentApprovalFromRuntimeEvent(t *testing.T) {
	db := setupBridgeDB(t)
	defer db.Close()
	publisher := &fakePublisher{}
	bridge := NewBridge(db, publisher)

	metadata, _ := json.Marshal(Ownership{TaskID: "task-1", AgentRunID: "run-1"})
	thread := agentruntime.Thread{
		ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex",
		Status: agentruntime.ThreadStatusActive, Metadata: metadata,
	}
	event := agentruntime.Event{
		Type: agentruntime.EventTypeItemStarted,
		ThreadID: thread.ID,
		TurnID: "turn-1",
		Status: agentruntime.TurnStatusPausedApproval,
		Item: &agentruntime.Item{
			ID: "item-1", ThreadID: thread.ID, TurnID: "turn-1",
			Type: agentruntime.ItemTypeApproval, Status: agentruntime.ItemStatusPending,
			Name: "item/commandExecution/requestApproval",
			Payload: json.RawMessage(`{"command":"go test ./..."}`),
		},
	}

	if err := bridge.HandleEvent(context.Background(), thread, event); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	if err := bridge.HandleEvent(context.Background(), thread, event); err != nil {
		t.Fatalf("HandleEvent(second) error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM approvals`).Scan(&count); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if count != 1 {
		t.Fatalf("approval count = %d, want 1", count)
	}

	var approvalType, requestedBy, runID, rawMetadata string
	if err := db.QueryRow(`SELECT approval_type, requested_by, agent_run_id, metadata FROM approvals LIMIT 1`).
		Scan(&approvalType, &requestedBy, &runID, &rawMetadata); err != nil {
		t.Fatalf("load approval: %v", err)
	}
	if approvalType != models.ApprovalTypeAgentRuntime || requestedBy != "user-1" || runID != "run-1" {
		t.Fatalf("approval = type:%q requested_by:%q run:%q", approvalType, requestedBy, runID)
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(rawMetadata), &stored); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if stored["thread_id"] != "thread-1" || stored["turn_id"] != "turn-1" || stored["item_id"] != "item-1" {
		t.Fatalf("metadata = %#v", stored)
	}
	if publisher.count != 1 || publisher.subject != events.ApprovalRequested {
		t.Fatalf("publisher = count:%d subject:%q", publisher.count, publisher.subject)
	}
}

func TestBridgeIgnoresNonApprovalEvents(t *testing.T) {
	db := setupBridgeDB(t)
	defer db.Close()
	publisher := &fakePublisher{}
	bridge := NewBridge(db, publisher)

	thread := agentruntime.Thread{ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex", Status: agentruntime.ThreadStatusActive}
	event := agentruntime.Event{Type: agentruntime.EventTypeTurnStarted, ThreadID: thread.ID, TurnID: "turn-1"}

	if err := bridge.HandleEvent(context.Background(), thread, event); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM approvals`).Scan(&count)
	if count != 0 || publisher.count != 0 {
		t.Fatalf("approval/publish count = %d/%d, want 0/0", count, publisher.count)
	}
}

func setupBridgeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			created_by TEXT NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE agent_runs (\n\t\t\tid TEXT PRIMARY KEY,\n\t\t\ttask_id TEXT NOT NULL,\n\t\t\tstatus TEXT NOT NULL,\n\t\t\terror_message TEXT,\n\t\t\tupdated_at DATETIME\n\t\t);
		CREATE TABLE approvals (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_run_id TEXT,
			approval_type TEXT NOT NULL,
			requested_by TEXT NOT NULL,
			requested_at DATETIME NOT NULL,
			metadata TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		INSERT INTO tasks (id, created_by) VALUES ('task-1', 'user-1');
		INSERT INTO agent_runs (id, task_id, status) VALUES ('run-1', 'task-1', 'running');
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}
