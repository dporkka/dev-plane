package agentruntime

import (
	"context"
	"testing"
	"time"
)

func TestPersistEventTracksApprovalPauseAndResume(t *testing.T) {
	store := newManagerStore()
	registry := NewRegistry()
	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	now := time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)
	thread := Thread{
		ID:          "thread-1",
		WorkspaceID: "workspace-1",
		Provider:    "codex",
		Status:      ThreadStatusActive,
	}
	turn := &Turn{
		ID:        "turn-1",
		ThreadID:  thread.ID,
		Status:    TurnStatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.PutTurn(context.Background(), *turn); err != nil {
		t.Fatalf("PutTurn() error = %v", err)
	}

	approval := Item{
		ID:       "approval-1",
		ThreadID: thread.ID,
		TurnID:   turn.ID,
		Type:     ItemTypeApproval,
		Status:   ItemStatusPending,
	}
	paused, err := manager.persistEvent(context.Background(), thread, turn, Event{
		Type:       EventTypeItemStarted,
		ThreadID:   thread.ID,
		TurnID:     turn.ID,
		Status:     TurnStatusPausedApproval,
		Item:       &approval,
		OccurredAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("persistEvent(pause) error = %v", err)
	}
	if paused == nil || paused.Status != TurnStatusPausedApproval {
		t.Fatalf("paused turn = %#v", paused)
	}

	command := Item{
		ID:       "command-1",
		ThreadID: thread.ID,
		TurnID:   turn.ID,
		Type:     ItemTypeCommand,
		Status:   ItemStatusRunning,
	}
	running, err := manager.persistEvent(context.Background(), thread, paused, Event{
		Type:       EventTypeItemStarted,
		ThreadID:   thread.ID,
		TurnID:     turn.ID,
		Status:     TurnStatusRunning,
		Item:       &command,
		OccurredAt: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("persistEvent(resume) error = %v", err)
	}
	if running == nil || running.Status != TurnStatusRunning {
		t.Fatalf("running turn = %#v", running)
	}
}
