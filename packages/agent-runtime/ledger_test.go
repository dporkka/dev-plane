package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

type recordingRuntimeEventLedger struct {
	records []RuntimeEventRecord
	err     error
}

func (l *recordingRuntimeEventLedger) AppendRuntimeEvent(_ context.Context, record RuntimeEventRecord) error {
	if l.err != nil {
		return l.err
	}
	l.records = append(l.records, record)
	return nil
}

func (l *recordingRuntimeEventLedger) ListRuntimeEvents(_ context.Context, _, _ string) ([]RuntimeEventRecord, error) {
	return append([]RuntimeEventRecord(nil), l.records...), nil
}

func TestLedgerEventSinkPersistsCanonicalRecord(t *testing.T) {
	ledger := &recordingRuntimeEventLedger{}
	sink, err := NewLedgerEventSink(ledger)
	if err != nil {
		t.Fatalf("NewLedgerEventSink() error = %v", err)
	}

	recordedAt := time.Date(2026, 10, 6, 14, 0, 0, 0, time.UTC)
	sink.now = func() time.Time { return recordedAt }
	occurredAt := recordedAt.Add(-time.Second)

	thread := Thread{
		ID:          "thread-1",
		WorkspaceID: "workspace-1",
		Provider:    "codex",
		Status:      ThreadStatusActive,
	}
	item := Item{
		ID:       "item-1",
		ThreadID: thread.ID,
		TurnID:   "turn-1",
		Type:     ItemTypeCommand,
		Status:   ItemStatusCompleted,
	}
	event := Event{
		Sequence:   7,
		Type:       EventTypeItemCompleted,
		ThreadID:   "provider-thread-id-must-not-win",
		TurnID:     "turn-1",
		Status:     TurnStatusRunning,
		Item:       &item,
		OccurredAt: occurredAt,
	}

	if err := sink.HandleEvent(context.Background(), thread, event); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	if len(ledger.records) != 1 {
		t.Fatalf("ledger records = %d, want 1", len(ledger.records))
	}

	got := ledger.records[0]
	if got.WorkspaceID != thread.WorkspaceID {
		t.Fatalf("WorkspaceID = %q, want %q", got.WorkspaceID, thread.WorkspaceID)
	}
	if got.ThreadID != thread.ID {
		t.Fatalf("ThreadID = %q, want %q", got.ThreadID, thread.ID)
	}
	if got.TurnID != event.TurnID {
		t.Fatalf("TurnID = %q, want %q", got.TurnID, event.TurnID)
	}
	if got.Provider != thread.Provider {
		t.Fatalf("Provider = %q, want %q", got.Provider, thread.Provider)
	}
	if got.Sequence != event.Sequence {
		t.Fatalf("Sequence = %d, want %d", got.Sequence, event.Sequence)
	}
	if got.Type != event.Type {
		t.Fatalf("Type = %q, want %q", got.Type, event.Type)
	}
	if !got.OccurredAt.Equal(occurredAt) {
		t.Fatalf("OccurredAt = %v, want %v", got.OccurredAt, occurredAt)
	}
	if !got.RecordedAt.Equal(recordedAt) {
		t.Fatalf("RecordedAt = %v, want %v", got.RecordedAt, recordedAt)
	}

	var persisted Event
	if err := json.Unmarshal(got.Payload, &persisted); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if persisted.ThreadID != thread.ID {
		t.Fatalf("persisted ThreadID = %q, want canonical %q", persisted.ThreadID, thread.ID)
	}
	if !reflect.DeepEqual(persisted.Item, event.Item) {
		t.Fatalf("persisted Item = %#v, want %#v", persisted.Item, event.Item)
	}
}

func TestLedgerEventSinkUsesRecordedTimeWhenProviderOmitsOccurredAt(t *testing.T) {
	ledger := &recordingRuntimeEventLedger{}
	sink, err := NewLedgerEventSink(ledger)
	if err != nil {
		t.Fatalf("NewLedgerEventSink() error = %v", err)
	}

	now := time.Date(2026, 10, 6, 14, 1, 0, 0, time.UTC)
	sink.now = func() time.Time { return now }
	thread := Thread{ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex", Status: ThreadStatusActive}
	event := Event{Sequence: 1, Type: EventTypeTurnStarted, TurnID: "turn-1", Status: TurnStatusRunning}

	if err := sink.HandleEvent(context.Background(), thread, event); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	if len(ledger.records) != 1 {
		t.Fatalf("ledger records = %d, want 1", len(ledger.records))
	}
	if !ledger.records[0].OccurredAt.Equal(now) {
		t.Fatalf("OccurredAt = %v, want %v", ledger.records[0].OccurredAt, now)
	}
	if !ledger.records[0].RecordedAt.Equal(now) {
		t.Fatalf("RecordedAt = %v, want %v", ledger.records[0].RecordedAt, now)
	}
}

func TestLedgerEventSinkRejectsInvalidEvent(t *testing.T) {
	ledger := &recordingRuntimeEventLedger{}
	sink, err := NewLedgerEventSink(ledger)
	if err != nil {
		t.Fatalf("NewLedgerEventSink() error = %v", err)
	}
	thread := Thread{ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex", Status: ThreadStatusActive}

	err = sink.HandleEvent(context.Background(), thread, Event{Sequence: 0, Type: EventTypeTurnStarted, TurnID: "turn-1"})
	if err == nil {
		t.Fatal("HandleEvent() error = nil, want invalid sequence error")
	}
	if len(ledger.records) != 0 {
		t.Fatalf("ledger records = %d, want 0", len(ledger.records))
	}
}

func TestLedgerEventSinkPropagatesAppendFailure(t *testing.T) {
	ledger := &recordingRuntimeEventLedger{err: errors.New("ledger unavailable")}
	sink, err := NewLedgerEventSink(ledger)
	if err != nil {
		t.Fatalf("NewLedgerEventSink() error = %v", err)
	}
	sink.now = func() time.Time { return time.Date(2026, 10, 6, 14, 2, 0, 0, time.UTC) }
	thread := Thread{ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex", Status: ThreadStatusActive}
	event := Event{Sequence: 1, Type: EventTypeTurnStarted, TurnID: "turn-1", Status: TurnStatusRunning}

	err = sink.HandleEvent(context.Background(), thread, event)
	if err == nil || err.Error() != "append runtime event: ledger unavailable" {
		t.Fatalf("HandleEvent() error = %v", err)
	}
}

func TestNewLedgerEventSinkRejectsNilLedger(t *testing.T) {
	if _, err := NewLedgerEventSink(nil); err == nil {
		t.Fatal("NewLedgerEventSink(nil) error = nil")
	}
}
