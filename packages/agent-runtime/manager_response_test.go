package agentruntime

import (
	"context"
	"testing"
)

type approvalManagerProvider struct {
	managerProvider
	response ApprovalResponse
}

func (p *approvalManagerProvider) Capabilities() CapabilitySet {
	return NewCapabilitySet(CapabilityResumeThread, CapabilityApprovalRequests)
}

func (p *approvalManagerProvider) RespondApproval(_ context.Context, response ApprovalResponse) error {
	p.response = response
	return nil
}

func TestManagerRespondApprovalResumesPausedTurn(t *testing.T) {
	store := newManagerStore()
	provider := &approvalManagerProvider{}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	thread := Thread{ID: "thread-1", WorkspaceID: "workspace-1", Provider: "codex", Status: ThreadStatusActive}
	turn := Turn{ID: "turn-1", ThreadID: thread.ID, Status: TurnStatusPausedApproval}
	item := Item{ID: "approval-1", ThreadID: thread.ID, TurnID: turn.ID, Type: ItemTypeApproval, Status: ItemStatusPending}
	if err := store.PutThread(context.Background(), thread); err != nil {
		t.Fatalf("PutThread() error = %v", err)
	}
	if err := store.PutTurn(context.Background(), turn); err != nil {
		t.Fatalf("PutTurn() error = %v", err)
	}
	if err := store.PutItem(context.Background(), item); err != nil {
		t.Fatalf("PutItem() error = %v", err)
	}

	err = manager.RespondApproval(context.Background(), ApprovalResponse{
		ThreadID: thread.ID,
		TurnID: turn.ID,
		ItemID: item.ID,
		Decision: ApprovalDecisionApproved,
		Note: "approved by owner",
	})
	if err != nil {
		t.Fatalf("RespondApproval() error = %v", err)
	}
	if provider.response.ItemID != item.ID || provider.response.Decision != ApprovalDecisionApproved {
		t.Fatalf("provider response = %#v", provider.response)
	}

	storedItem, err := store.GetItem(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetItem() error = %v", err)
	}
	if storedItem.Status != ItemStatusCompleted {
		t.Fatalf("item status = %q, want completed", storedItem.Status)
	}
	storedTurn, err := store.GetTurn(context.Background(), turn.ID)
	if err != nil {
		t.Fatalf("GetTurn() error = %v", err)
	}
	if storedTurn.Status != TurnStatusRunning {
		t.Fatalf("turn status = %q, want running", storedTurn.Status)
	}
}

func TestManagerRespondApprovalDenialCancelsApprovalItemAndResumesProvider(t *testing.T) {
	store := newManagerStore()
	provider := &approvalManagerProvider{}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	thread := Thread{ID: "thread-2", WorkspaceID: "workspace-1", Provider: "codex", Status: ThreadStatusActive}
	turn := Turn{ID: "turn-2", ThreadID: thread.ID, Status: TurnStatusPausedApproval}
	item := Item{ID: "approval-2", ThreadID: thread.ID, TurnID: turn.ID, Type: ItemTypeApproval, Status: ItemStatusPending}
	_ = store.PutThread(context.Background(), thread)
	_ = store.PutTurn(context.Background(), turn)
	_ = store.PutItem(context.Background(), item)

	err = manager.RespondApproval(context.Background(), ApprovalResponse{
		ThreadID: thread.ID,
		TurnID: turn.ID,
		ItemID: item.ID,
		Decision: ApprovalDecisionDenied,
	})
	if err != nil {
		t.Fatalf("RespondApproval() error = %v", err)
	}
	storedItem, _ := store.GetItem(context.Background(), item.ID)
	if storedItem.Status != ItemStatusCancelled {
		t.Fatalf("item status = %q, want cancelled", storedItem.Status)
	}
	storedTurn, _ := store.GetTurn(context.Background(), turn.ID)
	if storedTurn.Status != TurnStatusRunning {
		t.Fatalf("turn status = %q, want running", storedTurn.Status)
	}
}
