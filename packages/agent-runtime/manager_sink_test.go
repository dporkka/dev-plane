package agentruntime

import (
	"context"
	"errors"
	"testing"
)

type recordingRuntimeEventSink struct {
	threadID string
	event    Event
	err      error
}

func (s *recordingRuntimeEventSink) HandleEvent(_ context.Context, thread Thread, event Event) error {
	s.threadID = thread.ID
	s.event = event
	return s.err
}

func TestManagerEventSinkRunsAfterPersistenceBeforeDelivery(t *testing.T) {
	store := newManagerStore()
	provider := &managerProvider{}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	sink := &recordingRuntimeEventSink{}
	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	manager.WithEventSink(sink)

	thread, err := manager.CreateThread(context.Background(), "codex", CreateThreadRequest{WorkspaceID: "workspace-1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	stream, err := manager.RunTurn(context.Background(), RunTurnRequest{
		ThreadID: thread.ID,
		Input: TurnInput{Text: "test"},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	event := <-stream.Events
	if sink.threadID != thread.ID || sink.event.Type != event.Type {
		t.Fatalf("sink = thread:%q event:%q, delivered:%q", sink.threadID, sink.event.Type, event.Type)
	}
	if _, err := store.GetTurn(context.Background(), event.TurnID); err != nil {
		t.Fatalf("event reached sink before durable turn persistence: %v", err)
	}
}

func TestManagerEventSinkFailureStopsDelivery(t *testing.T) {
	store := newManagerStore()
	provider := &managerProvider{}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	manager.WithEventSink(&recordingRuntimeEventSink{err: errors.New("approval persistence failed")})

	thread, err := manager.CreateThread(context.Background(), "codex", CreateThreadRequest{WorkspaceID: "workspace-1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	stream, err := manager.RunTurn(context.Background(), RunTurnRequest{
		ThreadID: thread.ID,
		Input: TurnInput{Text: "test"},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if event, ok := <-stream.Events; ok {
		t.Fatalf("unexpected delivered event: %#v", event)
	}
	if err := <-stream.Errors; err == nil || err.Error() != "approval persistence failed" {
		t.Fatalf("stream error = %v", err)
	}
}
