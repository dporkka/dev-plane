package agentruntime

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type recordingChainSink struct {
	name  string
	calls *[]string
	err   error
}

func (s *recordingChainSink) HandleEvent(_ context.Context, _ Thread, _ Event) error {
	*s.calls = append(*s.calls, s.name)
	return s.err
}

func TestNewEventSinkChainRejectsEmptyChain(t *testing.T) {
	if _, err := NewEventSinkChain(); err == nil {
		t.Fatal("NewEventSinkChain() error = nil, want empty-chain error")
	}
}

func TestNewEventSinkChainRejectsNilSink(t *testing.T) {
	calls := []string{}
	if _, err := NewEventSinkChain(&recordingChainSink{name: "first", calls: &calls}, nil); err == nil {
		t.Fatal("NewEventSinkChain(..., nil) error = nil, want nil-sink error")
	}
}

func TestEventSinkChainRunsSinksInDeclarationOrder(t *testing.T) {
	calls := []string{}
	chain, err := NewEventSinkChain(
		&recordingChainSink{name: "ledger", calls: &calls},
		&recordingChainSink{name: "approval", calls: &calls},
		&recordingChainSink{name: "observer", calls: &calls},
	)
	if err != nil {
		t.Fatalf("NewEventSinkChain() error = %v", err)
	}

	thread := Thread{ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex", Status: ThreadStatusActive}
	event := Event{Sequence: 1, Type: EventTypeTurnStarted, TurnID: "turn-1", Status: TurnStatusRunning}
	if err := chain.HandleEvent(context.Background(), thread, event); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	want := []string{"ledger", "approval", "observer"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestEventSinkChainFailsClosedAtFirstError(t *testing.T) {
	calls := []string{}
	chain, err := NewEventSinkChain(
		&recordingChainSink{name: "ledger", calls: &calls},
		&recordingChainSink{name: "approval", calls: &calls, err: errors.New("approval unavailable")},
		&recordingChainSink{name: "observer", calls: &calls},
	)
	if err != nil {
		t.Fatalf("NewEventSinkChain() error = %v", err)
	}

	thread := Thread{ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex", Status: ThreadStatusActive}
	event := Event{Sequence: 1, Type: EventTypeTurnStarted, TurnID: "turn-1", Status: TurnStatusRunning}
	err = chain.HandleEvent(context.Background(), thread, event)
	if err == nil || err.Error() != "event sink 2: approval unavailable" {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	want := []string{"ledger", "approval"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestEventSinkChainCopiesInputSlice(t *testing.T) {
	calls := []string{}
	first := &recordingChainSink{name: "first", calls: &calls}
	second := &recordingChainSink{name: "second", calls: &calls}
	sinks := []EventSink{first, second}
	chain, err := NewEventSinkChain(sinks...)
	if err != nil {
		t.Fatalf("NewEventSinkChain() error = %v", err)
	}

	sinks[0] = &recordingChainSink{name: "mutated", calls: &calls}
	thread := Thread{ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex", Status: ThreadStatusActive}
	event := Event{Sequence: 1, Type: EventTypeTurnStarted, TurnID: "turn-1", Status: TurnStatusRunning}
	if err := chain.HandleEvent(context.Background(), thread, event); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	want := []string{"first", "second"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}
