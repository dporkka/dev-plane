package agentruntime

import (
	"context"
	"errors"
	"testing"
)

type runtimeSinkObservation struct {
	threadID string
	event    Event
}

type recordingRuntimeEventSink struct {
	err     error
	entered chan runtimeSinkObservation
	release chan struct{}
}

func (s *recordingRuntimeEventSink) HandleEvent(_ context.Context, thread Thread, event Event) error {
	if s.entered != nil {
		s.entered <- runtimeSinkObservation{threadID: thread.ID, event: event}
	}
	if s.release != nil {
		<-s.release
	}
	return s.err
}

func TestManagerEventSinkRunsAfterPersistenceBeforeDelivery(t *testing.T) {
	store := newManagerStore()
	provider := &managerProvider{}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	sink := &recordingRuntimeEventSink{
		entered: make(chan runtimeSinkObservation, 1),
		release: make(chan struct{}),
	}
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
		Input:    TurnInput{Text: "test"},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	observation := <-sink.entered
	if _, err := store.GetTurn(context.Background(), observation.event.TurnID); err != nil {
		t.Fatalf("event reached sink before durable turn persistence: %v", err)
	}
	close(sink.release)
	event := <-stream.Events
	if observation.threadID != thread.ID || observation.event.Type != event.Type {
		t.Fatalf("sink = thread:%q event:%q, delivered:%q", observation.threadID, observation.event.Type, event.Type)
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
		Input:    TurnInput{Text: "test"},
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
