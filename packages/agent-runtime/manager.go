package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrProviderCapabilityUnsupported is returned when a registered provider
	// does not implement an optional capability required by the requested operation.
	ErrProviderCapabilityUnsupported = errors.New("agent runtime provider capability unsupported")
)

// Manager coordinates provider adapters with durable storage.
// Provider-specific process state stays behind Provider; the Manager owns the
// durable Thread -> Turn -> Item lifecycle used by the control plane.
type Manager struct {
	registry *Registry
	store    Store
	now      func() time.Time
}

// RunStream is a provider event stream whose events have been durably recorded
// before being exposed to the caller.
type RunStream struct {
	Events <-chan Event
	Errors <-chan error
}

// NewManager creates a durable agent runtime manager.
func NewManager(registry *Registry, store Store) (*Manager, error) {
	if registry == nil {
		return nil, errors.New("agent runtime registry is required")
	}
	if store == nil {
		return nil, errors.New("agent runtime store is required")
	}
	return &Manager{
		registry: registry,
		store:    store,
		now:      func() time.Time { return time.Now().UTC() },
	}, nil
}

// CreateThread starts a provider thread and persists its durable identity before
// returning it to the caller.
func (m *Manager) CreateThread(ctx context.Context, providerName string, req CreateThreadRequest) (*Thread, error) {
	provider, err := m.registry.Get(providerName)
	if err != nil {
		return nil, err
	}
	thread, err := provider.CreateThread(ctx, req)
	if err != nil {
		return nil, err
	}
	if thread == nil {
		return nil, errors.New("agent runtime provider returned nil thread")
	}

	now := m.now()
	if strings.TrimSpace(thread.Provider) == "" {
		thread.Provider = provider.Name()
	}
	if thread.CreatedAt.IsZero() {
		thread.CreatedAt = now
	}
	if thread.UpdatedAt.IsZero() {
		thread.UpdatedAt = thread.CreatedAt
	}
	if err := thread.Validate(); err != nil {
		return nil, fmt.Errorf("validate provider thread: %w", err)
	}
	if err := m.store.PutThread(ctx, *thread); err != nil {
		return nil, fmt.Errorf("persist agent thread %s: %w", thread.ID, err)
	}
	return thread, nil
}

// ResumeThread reloads durable provider identity and reattaches to the provider-native thread.
func (m *Manager) ResumeThread(ctx context.Context, threadID string) (*Thread, error) {
	stored, err := m.store.GetThread(ctx, threadID)
	if err != nil {
		return nil, fmt.Errorf("load agent thread %s: %w", threadID, err)
	}
	provider, err := m.registry.Get(stored.Provider)
	if err != nil {
		return nil, err
	}
	resumer, ok := provider.(ResumeProvider)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not support resume_thread", ErrProviderCapabilityUnsupported, provider.Name())
	}

	resumed, err := resumer.ResumeThread(ctx, ResumeThreadRequest{
		ThreadID:         stored.ID,
		ProviderThreadID: stored.ProviderThreadID,
		WorkspaceID:      stored.WorkspaceID,
		Model:            stored.Model,
	})
	if err != nil {
		return nil, err
	}
	if resumed == nil {
		return nil, errors.New("agent runtime provider returned nil resumed thread")
	}

	resumed.ID = stored.ID
	resumed.WorkspaceID = firstValue(resumed.WorkspaceID, stored.WorkspaceID)
	resumed.Provider = firstValue(resumed.Provider, stored.Provider)
	resumed.ProviderThreadID = firstValue(resumed.ProviderThreadID, stored.ProviderThreadID)
	resumed.Model = firstValue(resumed.Model, stored.Model)
	if resumed.CreatedAt.IsZero() {
		resumed.CreatedAt = stored.CreatedAt
	}
	if resumed.UpdatedAt.IsZero() {
		resumed.UpdatedAt = m.now()
	}
	if err := resumed.Validate(); err != nil {
		return nil, fmt.Errorf("validate resumed thread: %w", err)
	}
	if err := m.store.PutThread(ctx, *resumed); err != nil {
		return nil, fmt.Errorf("persist resumed agent thread %s: %w", resumed.ID, err)
	}
	return resumed, nil
}

// RunTurn starts a provider turn and exposes only events that have first been
// durably recorded. Persistence failure cancels the provider context and closes
// the stream with an error rather than continuing in a partially recorded state.
func (m *Manager) RunTurn(ctx context.Context, req RunTurnRequest) (*RunStream, error) {
	thread, err := m.store.GetThread(ctx, req.ThreadID)
	if err != nil {
		return nil, fmt.Errorf("load agent thread %s: %w", req.ThreadID, err)
	}
	provider, err := m.registry.Get(thread.Provider)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.ProviderThreadID) == "" {
		req.ProviderThreadID = thread.ProviderThreadID
	}

	runCtx, cancel := context.WithCancel(ctx)
	providerEvents, err := provider.RunTurn(runCtx, req)
	if err != nil {
		cancel()
		return nil, err
	}

	events := make(chan Event, 32)
	errs := make(chan error, 1)
	go m.recordRun(runCtx, cancel, thread, providerEvents, events, errs)
	return &RunStream{Events: events, Errors: errs}, nil
}

func (m *Manager) recordRun(
	ctx context.Context,
	cancel context.CancelFunc,
	thread Thread,
	providerEvents <-chan Event,
	events chan<- Event,
	errs chan<- error,
) {
	defer cancel()
	defer close(events)
	defer close(errs)

	var currentTurn *Turn
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-providerEvents:
			if !ok {
				return
			}
			event.ThreadID = thread.ID
			nextTurn, err := m.persistEvent(ctx, thread, currentTurn, event)
			if err != nil {
				cancel()
				errs <- err
				return
			}
			currentTurn = nextTurn

			select {
			case <-ctx.Done():
				return
			case events <- event:
			}
		}
	}
}

func (m *Manager) persistEvent(ctx context.Context, thread Thread, currentTurn *Turn, event Event) (*Turn, error) {
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = m.now()
	}

	switch event.Type {
	case EventTypeTurnStarted:
		if strings.TrimSpace(event.TurnID) == "" {
			return currentTurn, errors.New("turn_started event is missing turn id")
		}
		status := event.Status
		if status == "" {
			status = TurnStatusRunning
		}
		startedAt := occurredAt
		turn := Turn{
			ID:             event.TurnID,
			ThreadID:       thread.ID,
			ProviderTurnID: firstValue(event.ProviderTurnID, event.TurnID),
			Status:         status,
			StartedAt:      &startedAt,
			CreatedAt:      occurredAt,
			UpdatedAt:      occurredAt,
		}
		if err := m.store.PutTurn(ctx, turn); err != nil {
			return currentTurn, fmt.Errorf("persist turn %s start: %w", turn.ID, err)
		}
		return &turn, nil

	case EventTypeItemStarted, EventTypeItemCompleted, EventTypeItemFailed:
		if event.Item == nil {
			return currentTurn, fmt.Errorf("%s event is missing item", event.Type)
		}
		item := *event.Item
		item.ThreadID = thread.ID
		item.TurnID = event.TurnID
		if item.CreatedAt.IsZero() {
			item.CreatedAt = occurredAt
		}
		item.UpdatedAt = occurredAt
		if err := m.store.PutItem(ctx, item); err != nil {
			return currentTurn, fmt.Errorf("persist item %s: %w", item.ID, err)
		}
		return currentTurn, nil

	case EventTypeTurnStatus, EventTypeTurnCompleted, EventTypeTurnFailed:
		if strings.TrimSpace(event.TurnID) == "" {
			return currentTurn, fmt.Errorf("%s event is missing turn id", event.Type)
		}
		var turn Turn
		if currentTurn != nil && currentTurn.ID == event.TurnID {
			turn = *currentTurn
		} else {
			turn = Turn{
				ID:             event.TurnID,
				ThreadID:       thread.ID,
				ProviderTurnID: firstValue(event.ProviderTurnID, event.TurnID),
				CreatedAt:      occurredAt,
			}
		}
		if event.Status != "" {
			turn.Status = event.Status
		}
		turn.UpdatedAt = occurredAt
		if terminalTurnStatus(turn.Status) {
			completedAt := occurredAt
			turn.CompletedAt = &completedAt
		}
		if err := m.store.PutTurn(ctx, turn); err != nil {
			return currentTurn, fmt.Errorf("persist turn %s status: %w", turn.ID, err)
		}
		return &turn, nil
	default:
		return currentTurn, nil
	}
}

func terminalTurnStatus(status TurnStatus) bool {
	switch status {
	case TurnStatusCompleted, TurnStatusFailed, TurnStatusInterrupted:
		return true
	default:
		return false
	}
}

func firstValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
