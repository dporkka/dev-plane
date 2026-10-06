package db

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

func TestAgentRuntimeEventLedgerRoundTrip(t *testing.T) {
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	thread := agentruntime.Thread{
		ID:          "thread-ledger",
		WorkspaceID: "workspace-ledger",
		Provider:    "codex",
		Status:      agentruntime.ThreadStatusActive,
	}
	turn := agentruntime.Turn{
		ID:       "turn-ledger",
		ThreadID: thread.ID,
		Status:   agentruntime.TurnStatusRunning,
	}
	if err := database.PutThread(context.Background(), thread); err != nil {
		t.Fatalf("PutThread() error = %v", err)
	}
	if err := database.PutTurn(context.Background(), turn); err != nil {
		t.Fatalf("PutTurn() error = %v", err)
	}

	occurredAt := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	recordedAt := occurredAt.Add(time.Second)
	event := agentruntime.Event{
		Sequence:   2,
		Type:       agentruntime.EventTypeTurnStatus,
		ThreadID:   thread.ID,
		TurnID:     turn.ID,
		Status:     agentruntime.TurnStatusPausedApproval,
		OccurredAt: occurredAt,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	record := agentruntime.RuntimeEventRecord{
		WorkspaceID: thread.WorkspaceID,
		ThreadID:    thread.ID,
		TurnID:      turn.ID,
		Provider:    thread.Provider,
		Sequence:    event.Sequence,
		Type:        event.Type,
		Payload:     payload,
		OccurredAt:  occurredAt,
		RecordedAt:  recordedAt,
	}

	if err := database.AppendRuntimeEvent(context.Background(), record); err != nil {
		t.Fatalf("AppendRuntimeEvent() error = %v", err)
	}
	// Event delivery can be retried after a control-plane restart. Re-appending the
	// same provider sequence must be idempotent rather than duplicating history.
	if err := database.AppendRuntimeEvent(context.Background(), record); err != nil {
		t.Fatalf("AppendRuntimeEvent(duplicate) error = %v", err)
	}

	got, err := database.ListRuntimeEvents(context.Background(), thread.ID, turn.ID)
	if err != nil {
		t.Fatalf("ListRuntimeEvents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListRuntimeEvents() length = %d, want 1", len(got))
	}
	if !reflect.DeepEqual(got[0], record) {
		t.Fatalf("ListRuntimeEvents()[0] = %#v, want %#v", got[0], record)
	}
}

func TestAgentRuntimeEventLedgerListsWholeThreadInSequenceOrder(t *testing.T) {
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	thread := agentruntime.Thread{ID: "thread-order", WorkspaceID: "workspace-order", Provider: "codex", Status: agentruntime.ThreadStatusActive}
	if err := database.PutThread(context.Background(), thread); err != nil {
		t.Fatalf("PutThread() error = %v", err)
	}
	for _, turnID := range []string{"turn-a", "turn-b"} {
		if err := database.PutTurn(context.Background(), agentruntime.Turn{ID: turnID, ThreadID: thread.ID, Status: agentruntime.TurnStatusRunning}); err != nil {
			t.Fatalf("PutTurn(%s) error = %v", turnID, err)
		}
	}

	base := time.Date(2026, 10, 6, 15, 5, 0, 0, time.UTC)
	records := []agentruntime.RuntimeEventRecord{
		newRuntimeEventRecordForTest(t, thread, "turn-b", 2, base.Add(4*time.Second)),
		newRuntimeEventRecordForTest(t, thread, "turn-a", 2, base.Add(2*time.Second)),
		newRuntimeEventRecordForTest(t, thread, "turn-b", 1, base.Add(3*time.Second)),
		newRuntimeEventRecordForTest(t, thread, "turn-a", 1, base.Add(time.Second)),
	}
	for _, record := range records {
		if err := database.AppendRuntimeEvent(context.Background(), record); err != nil {
			t.Fatalf("AppendRuntimeEvent() error = %v", err)
		}
	}

	got, err := database.ListRuntimeEvents(context.Background(), thread.ID, "")
	if err != nil {
		t.Fatalf("ListRuntimeEvents(thread) error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("ListRuntimeEvents(thread) length = %d, want 4", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].OccurredAt.Before(got[i-1].OccurredAt) {
			t.Fatalf("events are not in occurrence order: %#v", got)
		}
	}
}

func TestAgentRuntimeEventLedgerRejectsInvalidRecord(t *testing.T) {
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	err = database.AppendRuntimeEvent(context.Background(), agentruntime.RuntimeEventRecord{})
	if err == nil {
		t.Fatal("AppendRuntimeEvent(empty) error = nil")
	}
}

func newRuntimeEventRecordForTest(t *testing.T, thread agentruntime.Thread, turnID string, sequence int64, occurredAt time.Time) agentruntime.RuntimeEventRecord {
	t.Helper()
	event := agentruntime.Event{
		Sequence:   sequence,
		Type:       agentruntime.EventTypeTurnStatus,
		ThreadID:   thread.ID,
		TurnID:     turnID,
		Status:     agentruntime.TurnStatusRunning,
		OccurredAt: occurredAt,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return agentruntime.RuntimeEventRecord{
		WorkspaceID: thread.WorkspaceID,
		ThreadID:    thread.ID,
		TurnID:      turnID,
		Provider:    thread.Provider,
		Sequence:    sequence,
		Type:        event.Type,
		Payload:     payload,
		OccurredAt:  occurredAt,
		RecordedAt:  occurredAt.Add(time.Millisecond),
	}
}
