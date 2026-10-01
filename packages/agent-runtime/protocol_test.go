package agentruntime

import (
	"context"
	"errors"
	"testing"
)

type fakeProvider struct {
	name string
	caps CapabilitySet
}

func (f fakeProvider) Name() string { return f.name }

func (f fakeProvider) Capabilities() CapabilitySet { return f.caps }

func (f fakeProvider) CreateThread(_ context.Context, req CreateThreadRequest) (*Thread, error) {
	return &Thread{
		ID:          "thread-1",
		WorkspaceID: req.WorkspaceID,
		Provider:    f.name,
		Status:      ThreadStatusActive,
	}, nil
}

func (f fakeProvider) RunTurn(_ context.Context, req RunTurnRequest) (<-chan Event, error) {
	ch := make(chan Event)
	close(ch)
	return ch, nil
}

type interruptProvider struct {
	fakeProvider
}

func (interruptProvider) InterruptTurn(_ context.Context, _ InterruptTurnRequest) error {
	return nil
}

func TestCapabilitySetSupportsOnlyDeclaredCapabilities(t *testing.T) {
	caps := NewCapabilitySet(
		CapabilityResumeThread,
		CapabilityInterruptTurn,
		CapabilityStructuredOutput,
	)

	if !caps.Supports(CapabilityResumeThread) {
		t.Fatal("resume_thread should be supported")
	}
	if !caps.Supports(CapabilityInterruptTurn) {
		t.Fatal("interrupt_turn should be supported")
	}
	if caps.Supports(CapabilityRollback) {
		t.Fatal("rollback should not be supported")
	}
}

func TestCanTransitionTurnStatus(t *testing.T) {
	tests := []struct {
		name string
		from TurnStatus
		to   TurnStatus
		want bool
	}{
		{"queued starts", TurnStatusQueued, TurnStatusRunning, true},
		{"running waits for approval", TurnStatusRunning, TurnStatusPausedApproval, true},
		{"running waits for input", TurnStatusRunning, TurnStatusPausedInput, true},
		{"approval resumes", TurnStatusPausedApproval, TurnStatusRunning, true},
		{"input resumes", TurnStatusPausedInput, TurnStatusRunning, true},
		{"interrupted recovers", TurnStatusInterrupted, TurnStatusRecovering, true},
		{"recovery resumes", TurnStatusRecovering, TurnStatusRunning, true},
		{"running completes", TurnStatusRunning, TurnStatusCompleted, true},
		{"running fails", TurnStatusRunning, TurnStatusFailed, true},
		{"completed is terminal", TurnStatusCompleted, TurnStatusRunning, false},
		{"failed is terminal", TurnStatusFailed, TurnStatusRecovering, false},
		{"same state is not a transition", TurnStatusRunning, TurnStatusRunning, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanTransitionTurnStatus(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanTransitionTurnStatus(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestThreadTurnAndItemValidation(t *testing.T) {
	thread := Thread{
		ID:          "thread-1",
		WorkspaceID: "workspace-1",
		Provider:    "codex",
		Status:      ThreadStatusActive,
	}
	if err := thread.Validate(); err != nil {
		t.Fatalf("Thread.Validate() error: %v", err)
	}

	turn := Turn{
		ID:       "turn-1",
		ThreadID: thread.ID,
		Status:   TurnStatusRunning,
	}
	if err := turn.Validate(); err != nil {
		t.Fatalf("Turn.Validate() error: %v", err)
	}

	item := Item{
		ID:       "item-1",
		ThreadID: thread.ID,
		TurnID:   turn.ID,
		Type:     ItemTypeToolCall,
		Status:   ItemStatusRunning,
	}
	if err := item.Validate(); err != nil {
		t.Fatalf("Item.Validate() error: %v", err)
	}
}

func TestValidationRejectsMissingOwnershipIdentifiers(t *testing.T) {
	if err := (Thread{ID: "thread-1", Provider: "codex", Status: ThreadStatusActive}).Validate(); err == nil {
		t.Fatal("Thread.Validate() error = nil, want missing workspace error")
	}
	if err := (Turn{ID: "turn-1", Status: TurnStatusRunning}).Validate(); err == nil {
		t.Fatal("Turn.Validate() error = nil, want missing thread error")
	}
	if err := (Item{ID: "item-1", ThreadID: "thread-1", Type: ItemTypeMessage, Status: ItemStatusCompleted}).Validate(); err == nil {
		t.Fatal("Item.Validate() error = nil, want missing turn error")
	}
}

func TestRegistryNormalizesProviderNames(t *testing.T) {
	registry := NewRegistry()
	provider := fakeProvider{name: " Codex ", caps: NewCapabilitySet(CapabilityStructuredOutput)}

	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	got, err := registry.Get("CODEX")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got != provider {
		t.Fatalf("Get() provider = %#v, want %#v", got, provider)
	}
}

func TestRegistryRejectsDuplicateProviderNames(t *testing.T) {
	registry := NewRegistry()
	provider := fakeProvider{name: "codex"}

	if err := registry.Register(provider); err != nil {
		t.Fatalf("first Register() error: %v", err)
	}
	if err := registry.Register(provider); !errors.Is(err, ErrProviderAlreadyRegistered) {
		t.Fatalf("second Register() error = %v, want ErrProviderAlreadyRegistered", err)
	}
}

func TestRegistryRejectsCapabilityInterfaceMismatch(t *testing.T) {
	registry := NewRegistry()
	provider := fakeProvider{
		name: "codex",
		caps: NewCapabilitySet(CapabilityInterruptTurn),
	}

	if err := registry.Register(provider); !errors.Is(err, ErrCapabilityContract) {
		t.Fatalf("Register() error = %v, want ErrCapabilityContract", err)
	}
}

func TestRegistryAcceptsCapabilityWhenProviderImplementsInterface(t *testing.T) {
	registry := NewRegistry()
	provider := interruptProvider{
		fakeProvider: fakeProvider{
			name: "codex",
			caps: NewCapabilitySet(CapabilityInterruptTurn),
		},
	}

	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error: %v", err)
	}
}

func TestRegistryReturnsProviderNotFound(t *testing.T) {
	registry := NewRegistry()

	_, err := registry.Get("missing")
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("Get() error = %v, want ErrProviderNotFound", err)
	}
}
