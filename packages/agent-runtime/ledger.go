package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RuntimeEventRecord is the immutable control-plane envelope for one normalized
// provider event. The full normalized Event is retained in Payload while the
// ownership and ordering fields remain directly queryable.
type RuntimeEventRecord struct {
	WorkspaceID string          `json:"workspace_id"`
	ThreadID    string          `json:"thread_id"`
	TurnID      string          `json:"turn_id,omitempty"`
	Provider    string          `json:"provider"`
	Sequence    int64           `json:"sequence"`
	Type        EventType       `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	OccurredAt  time.Time       `json:"occurred_at"`
	RecordedAt  time.Time       `json:"recorded_at"`
}

// Validate checks the ownership, ordering, and payload invariants required for
// replayable runtime history.
func (r RuntimeEventRecord) Validate() error {
	switch {
	case strings.TrimSpace(r.WorkspaceID) == "":
		return errors.New("runtime event workspace id is required")
	case strings.TrimSpace(r.ThreadID) == "":
		return errors.New("runtime event thread id is required")
	case strings.TrimSpace(r.Provider) == "":
		return errors.New("runtime event provider is required")
	case r.Sequence <= 0:
		return fmt.Errorf("runtime event sequence must be positive: %d", r.Sequence)
	case !validEventType(r.Type):
		return fmt.Errorf("invalid runtime event type %q", r.Type)
	case len(r.Payload) == 0:
		return errors.New("runtime event payload is required")
	case !json.Valid(r.Payload):
		return errors.New("runtime event payload must be valid json")
	case r.OccurredAt.IsZero():
		return errors.New("runtime event occurred_at is required")
	case r.RecordedAt.IsZero():
		return errors.New("runtime event recorded_at is required")
	default:
		return nil
	}
}

// RuntimeEventLedger stores immutable provider-neutral event history. Append
// must be idempotent for an already-recorded (thread, turn, sequence) tuple so
// callers can safely retry delivery after control-plane restarts.
type RuntimeEventLedger interface {
	AppendRuntimeEvent(ctx context.Context, record RuntimeEventRecord) error
	ListRuntimeEvents(ctx context.Context, threadID, turnID string) ([]RuntimeEventRecord, error)
}

// LedgerEventSink adapts a RuntimeEventLedger to Manager's EventSink boundary.
// This lets callers add append-only history without coupling Manager to a
// concrete database implementation.
type LedgerEventSink struct {
	ledger RuntimeEventLedger
	now    func() time.Time
}

// NewLedgerEventSink creates a fail-closed event sink backed by a durable ledger.
func NewLedgerEventSink(ledger RuntimeEventLedger) (*LedgerEventSink, error) {
	if ledger == nil {
		return nil, errors.New("runtime event ledger is required")
	}
	return &LedgerEventSink{
		ledger: ledger,
		now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

// HandleEvent normalizes ownership, serializes the complete event, and appends
// it to the immutable ledger. Any append failure is returned to Manager, which
// stops the provider stream rather than exposing unrecorded history.
func (s *LedgerEventSink) HandleEvent(ctx context.Context, thread Thread, event Event) error {
	if s == nil || s.ledger == nil {
		return errors.New("runtime event ledger sink is not configured")
	}
	if err := thread.Validate(); err != nil {
		return fmt.Errorf("validate runtime event thread: %w", err)
	}
	if event.Sequence <= 0 {
		return fmt.Errorf("runtime event sequence must be positive: %d", event.Sequence)
	}
	if !validEventType(event.Type) {
		return fmt.Errorf("invalid runtime event type %q", event.Type)
	}

	now := s.now()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// The durable control-plane thread identity is canonical even when an adapter
	// accidentally leaves a provider-native identifier on the event.
	event.ThreadID = thread.ID
	if event.OccurredAt.IsZero() {
		event.OccurredAt = now
	}
	if event.Item != nil {
		item := *event.Item
		item.ThreadID = thread.ID
		if strings.TrimSpace(item.TurnID) == "" {
			item.TurnID = event.TurnID
		}
		event.Item = &item
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal runtime event: %w", err)
	}
	record := RuntimeEventRecord{
		WorkspaceID: thread.WorkspaceID,
		ThreadID:    thread.ID,
		TurnID:      event.TurnID,
		Provider:    thread.Provider,
		Sequence:    event.Sequence,
		Type:        event.Type,
		Payload:     payload,
		OccurredAt:  event.OccurredAt,
		RecordedAt:  now,
	}
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate runtime event record: %w", err)
	}
	if err := s.ledger.AppendRuntimeEvent(ctx, record); err != nil {
		return fmt.Errorf("append runtime event: %w", err)
	}
	return nil
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventTypeThreadCreated,
		EventTypeTurnStarted,
		EventTypeTurnStatus,
		EventTypeItemStarted,
		EventTypeItemCompleted,
		EventTypeItemFailed,
		EventTypeTurnCompleted,
		EventTypeTurnFailed:
		return true
	default:
		return false
	}
}

var _ EventSink = (*LedgerEventSink)(nil)
