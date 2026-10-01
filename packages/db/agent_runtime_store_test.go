package db

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

var _ agentruntime.Store = (*DB)(nil)

func TestAgentRuntimeStoreRoundTrip(t *testing.T) {
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	started := time.Date(2026, 9, 30, 19, 30, 0, 0, time.UTC)
	completed := started.Add(time.Minute)

	thread := agentruntime.Thread{
		ID:               "thread-1",
		WorkspaceID:      "workspace-1",
		Provider:         "codex",
		ProviderThreadID: "0199-codex-thread",
		Model:            "gpt-5.6-codex",
		Status:           agentruntime.ThreadStatusActive,
		Metadata:         json.RawMessage(`{"source":"test"}`),
		CreatedAt:        started,
		UpdatedAt:        started,
	}
	turn := agentruntime.Turn{
		ID:             "turn-1",
		ThreadID:       thread.ID,
		ProviderTurnID: "0199-codex-turn",
		Status:         agentruntime.TurnStatusCompleted,
		StartedAt:      &started,
		CompletedAt:    &completed,
		CreatedAt:      started,
		UpdatedAt:      completed,
	}
	item := agentruntime.Item{
		ID:             "item-1",
		ThreadID:       thread.ID,
		TurnID:         turn.ID,
		ProviderItemID: "codex-item-1",
		Type:           agentruntime.ItemTypeCommand,
		Status:         agentruntime.ItemStatusCompleted,
		Name:           "commandExecution",
		Payload:        json.RawMessage(`{"command":"go test ./..."}`),
		CreatedAt:      started,
		UpdatedAt:      completed,
	}

	if err := database.PutThread(context.Background(), thread); err != nil {
		t.Fatalf("PutThread() error = %v", err)
	}
	if err := database.PutTurn(context.Background(), turn); err != nil {
		t.Fatalf("PutTurn() error = %v", err)
	}
	if err := database.PutItem(context.Background(), item); err != nil {
		t.Fatalf("PutItem() error = %v", err)
	}

	gotThread, err := database.GetThread(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	gotTurn, err := database.GetTurn(context.Background(), turn.ID)
	if err != nil {
		t.Fatalf("GetTurn() error = %v", err)
	}
	gotItem, err := database.GetItem(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetItem() error = %v", err)
	}

	if !reflect.DeepEqual(gotThread, thread) {
		t.Fatalf("GetThread() = %#v, want %#v", gotThread, thread)
	}
	if !reflect.DeepEqual(gotTurn, turn) {
		t.Fatalf("GetTurn() = %#v, want %#v", gotTurn, turn)
	}
	if !reflect.DeepEqual(gotItem, item) {
		t.Fatalf("GetItem() = %#v, want %#v", gotItem, item)
	}

	turns, err := database.ListTurns(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("ListTurns() error = %v", err)
	}
	if len(turns) != 1 || turns[0].ID != turn.ID {
		t.Fatalf("ListTurns() = %#v", turns)
	}
	items, err := database.ListItems(context.Background(), turn.ID)
	if err != nil {
		t.Fatalf("ListItems() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("ListItems() = %#v", items)
	}
}

func TestAgentRuntimeStoreUpsertsTurnStatus(t *testing.T) {
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	thread := agentruntime.Thread{
		ID:          "thread-upsert",
		WorkspaceID: "workspace-1",
		Provider:    "codex",
		Status:      agentruntime.ThreadStatusActive,
	}
	if err := database.PutThread(context.Background(), thread); err != nil {
		t.Fatalf("PutThread() error = %v", err)
	}

	turn := agentruntime.Turn{
		ID:       "turn-upsert",
		ThreadID: thread.ID,
		Status:   agentruntime.TurnStatusRunning,
	}
	if err := database.PutTurn(context.Background(), turn); err != nil {
		t.Fatalf("PutTurn(running) error = %v", err)
	}
	turn.Status = agentruntime.TurnStatusCompleted
	if err := database.PutTurn(context.Background(), turn); err != nil {
		t.Fatalf("PutTurn(completed) error = %v", err)
	}

	got, err := database.GetTurn(context.Background(), turn.ID)
	if err != nil {
		t.Fatalf("GetTurn() error = %v", err)
	}
	if got.Status != agentruntime.TurnStatusCompleted {
		t.Fatalf("turn status = %q, want completed", got.Status)
	}
}
