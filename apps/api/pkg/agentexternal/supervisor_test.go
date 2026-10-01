package agentexternal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
	"github.com/ai-dev-control-plane/events"
)

type fakeRuntimeManager struct {
	thread    *agentruntime.Thread
	stream    *agentruntime.RunStream
	createReq agentruntime.CreateThreadRequest
	runReq    agentruntime.RunTurnRequest
	resumeID  string
}

func (m *fakeRuntimeManager) CreateThread(_ context.Context, _ string, req agentruntime.CreateThreadRequest) (*agentruntime.Thread, error) {
	m.createReq = req
	if m.thread == nil {
		return nil, errors.New("thread not configured")
	}
	copy := *m.thread
	return &copy, nil
}

func (m *fakeRuntimeManager) ResumeThread(_ context.Context, threadID string) (*agentruntime.Thread, error) {
	m.resumeID = threadID
	if m.thread == nil {
		return nil, errors.New("thread not configured")
	}
	copy := *m.thread
	return &copy, nil
}

func (m *fakeRuntimeManager) RunTurn(_ context.Context, req agentruntime.RunTurnRequest) (*agentruntime.RunStream, error) {
	m.runReq = req
	return m.stream, nil
}

type fakeCompletionGate struct {
	err    error
	called chan CompletionRequest
}

func (g *fakeCompletionGate) VerifyExternalRunCompletion(_ context.Context, req CompletionRequest) error {
	if g.called != nil {
		g.called <- req
	}
	return g.err
}

type publishedEvent struct {
	subject string
	data    []byte
}

type fakeExternalPublisher struct {
	calls chan publishedEvent
}

func (p *fakeExternalPublisher) Publish(subject string, data []byte) error {
	if p.calls != nil {
		p.calls <- publishedEvent{subject: subject, data: append([]byte(nil), data...)}
	}
	return nil
}

func TestSupervisorSuspendsSchedulerWhileTurnContinuesToVerifiedCompletion(t *testing.T) {
	db := setupSupervisorDB(t)
	defer db.Close()

	eventCh := make(chan agentruntime.Event, 8)
	errCh := make(chan error, 1)
	manager := &fakeRuntimeManager{
		thread: &agentruntime.Thread{
			ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex",
			Status: agentruntime.ThreadStatusActive,
		},
		stream: &agentruntime.RunStream{Events: eventCh, Errors: errCh},
	}
	gate := &fakeCompletionGate{called: make(chan CompletionRequest, 1)}
	publisher := &fakeExternalPublisher{calls: make(chan publishedEvent, 4)}
	supervisor := NewSupervisor(db, manager, gate, publisher)

	eventCh <- agentruntime.Event{
		Type: agentruntime.EventTypeTurnStarted, ThreadID: "thread-1", TurnID: "turn-1",
		Status: agentruntime.TurnStatusRunning,
	}
	eventCh <- agentruntime.Event{
		Type: agentruntime.EventTypeItemStarted, ThreadID: "thread-1", TurnID: "turn-1",
		Status: agentruntime.TurnStatusPausedApproval,
		Item: &agentruntime.Item{
			ID: "approval-1", ThreadID: "thread-1", TurnID: "turn-1",
			Type: agentruntime.ItemTypeApproval, Status: agentruntime.ItemStatusPending,
		},
	}

	err := supervisor.ExecuteExternalRun(context.Background(), "run-1")
	if err == nil {
		t.Fatal("ExecuteExternalRun() error = nil, want durable suspension")
	}
	var suspended interface{ RunSuspended() bool }
	if !errors.As(err, &suspended) || !suspended.RunSuspended() {
		t.Fatalf("ExecuteExternalRun() error = %v, want suspension", err)
	}
	if manager.createReq.WorkspaceID != "workspace-1" {
		t.Fatalf("create workspace = %q", manager.createReq.WorkspaceID)
	}
	if manager.runReq.ThreadID != "thread-1" || !strings.Contains(manager.runReq.Input.Text, "Implement external runtime feature") {
		t.Fatalf("run request = %#v", manager.runReq)
	}

	var metadataRaw string
	if err := db.QueryRow(`SELECT metadata FROM agent_runs WHERE id = 'run-1'`).Scan(&metadataRaw); err != nil {
		t.Fatalf("load run metadata: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataRaw), &metadata); err != nil {
		t.Fatalf("decode run metadata: %v", err)
	}
	execution, _ := metadata["execution"].(map[string]any)
	if execution["thread_id"] != "thread-1" {
		t.Fatalf("execution metadata = %#v", execution)
	}

	eventCh <- agentruntime.Event{
		Type: agentruntime.EventTypeTurnCompleted, ThreadID: "thread-1", TurnID: "turn-1",
		Status: agentruntime.TurnStatusCompleted,
	}
	close(eventCh)
	close(errCh)

	select {
	case req := <-gate.called:
		if req.RunID != "run-1" || req.ThreadID != "thread-1" || req.TurnID != "turn-1" {
			t.Fatalf("completion request = %#v", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("completion gate was not called")
	}

	var completed publishedEvent
	select {
	case completed = <-publisher.calls:
		if completed.subject == events.AgentRunStarted {
			select {
			case completed = <-publisher.calls:
			case <-time.After(2 * time.Second):
				t.Fatal("completion event was not published")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run lifecycle event was not published")
	}
	if completed.subject != events.AgentRunCompleted {
		t.Fatalf("published subject = %q, want %q", completed.subject, events.AgentRunCompleted)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-1'`).Scan(&status); err != nil {
		t.Fatalf("load run status: %v", err)
	}
	if status != "completed" {
		t.Fatalf("run status = %q, want completed", status)
	}
}

func TestSupervisorFailsRunWhenCompletionGateRejectsProviderSuccess(t *testing.T) {
	db := setupSupervisorDB(t)
	defer db.Close()

	eventCh := make(chan agentruntime.Event, 4)
	errCh := make(chan error, 1)
	manager := &fakeRuntimeManager{
		thread: &agentruntime.Thread{
			ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex",
			Status: agentruntime.ThreadStatusActive,
		},
		stream: &agentruntime.RunStream{Events: eventCh, Errors: errCh},
	}
	gate := &fakeCompletionGate{err: errors.New("verification evidence missing"), called: make(chan CompletionRequest, 1)}
	publisher := &fakeExternalPublisher{calls: make(chan publishedEvent, 4)}
	supervisor := NewSupervisor(db, manager, gate, publisher)

	eventCh <- agentruntime.Event{
		Type: agentruntime.EventTypeTurnCompleted, ThreadID: "thread-1", TurnID: "turn-1",
		Status: agentruntime.TurnStatusCompleted,
	}
	close(eventCh)
	close(errCh)

	err := supervisor.ExecuteExternalRun(context.Background(), "run-1")
	if err == nil || !strings.Contains(err.Error(), "verification evidence missing") {
		t.Fatalf("ExecuteExternalRun() error = %v", err)
	}

	var status, errorMessage string
	if err := db.QueryRow(`SELECT status, error_message FROM agent_runs WHERE id = 'run-1'`).Scan(&status, &errorMessage); err != nil {
		t.Fatalf("load failed run: %v", err)
	}
	if status != "failed" || !strings.Contains(errorMessage, "verification evidence missing") {
		t.Fatalf("run = status:%q error:%q", status, errorMessage)
	}
}

func setupSupervisorDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT,
			spec TEXT,
			acceptance_criteria TEXT,
			deleted_at DATETIME
		);
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT,
			agent_role TEXT NOT NULL,
			model TEXT,
			status TEXT NOT NULL,
			metadata TEXT,
			started_at DATETIME,
			completed_at DATETIME,
			error_message TEXT,
			summary TEXT,
			updated_at DATETIME
		);
		INSERT INTO tasks (
			id, title, description, spec, acceptance_criteria
		) VALUES (
			'task-1',
			'Implement external runtime feature',
			'Use the durable provider runtime.',
			'{"summary":"external agent"}',
			'["tests pass"]'
		);
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, status, metadata
		) VALUES (
			'run-1', 'task-1', 'workspace-1', 'implementer', 'gpt-5.6-codex', 'admitting',
			'{"admission":{"policy":"task-readiness-v1"},"execution":{"backend":"agent_runtime","provider":"codex"}}'
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create supervisor fixture: %v", err)
	}
	return db
}
