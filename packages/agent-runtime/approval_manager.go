package agentruntime

import (
	"context"
	"fmt"
	"strings"
)

// RespondApproval answers one durable provider approval request and advances the
// durable item/turn state only through the provider-neutral contract.
func (m *Manager) RespondApproval(ctx context.Context, response ApprovalResponse) error {
	if m == nil {
		return fmt.Errorf("agent runtime manager is nil")
	}
	if strings.TrimSpace(response.ThreadID) == "" || strings.TrimSpace(response.TurnID) == "" || strings.TrimSpace(response.ItemID) == "" {
		return fmt.Errorf("approval thread_id, turn_id, and item_id are required")
	}
	if response.Decision != ApprovalDecisionApproved && response.Decision != ApprovalDecisionDenied {
		return fmt.Errorf("invalid approval decision %q", response.Decision)
	}

	thread, err := m.store.GetThread(ctx, response.ThreadID)
	if err != nil {
		return fmt.Errorf("load approval thread %s: %w", response.ThreadID, err)
	}
	turn, err := m.store.GetTurn(ctx, response.TurnID)
	if err != nil {
		return fmt.Errorf("load approval turn %s: %w", response.TurnID, err)
	}
	item, err := m.store.GetItem(ctx, response.ItemID)
	if err != nil {
		return fmt.Errorf("load approval item %s: %w", response.ItemID, err)
	}
	if turn.ThreadID != thread.ID || item.ThreadID != thread.ID || item.TurnID != turn.ID {
		return fmt.Errorf("approval ownership mismatch")
	}
	if item.Type != ItemTypeApproval {
		return fmt.Errorf("item %s is not an approval item", item.ID)
	}
	if item.Status == ItemStatusCompleted || item.Status == ItemStatusCancelled {
		return nil
	}
	if item.Status != ItemStatusPending {
		return fmt.Errorf("approval item %s is in indeterminate status %q", item.ID, item.Status)
	}
	if turn.Status != TurnStatusPausedApproval {
		return fmt.Errorf("turn %s is not paused for approval", turn.ID)
	}

	provider, err := m.registry.Get(thread.Provider)
	if err != nil {
		return err
	}
	responder, ok := provider.(ApprovalProvider)
	if !ok {
		return fmt.Errorf("%w: %s does not support approval_requests", ErrProviderCapabilityUnsupported, provider.Name())
	}

	now := m.now()
	inflight := item
	inflight.Status = ItemStatusRunning
	inflight.UpdatedAt = now
	if err := m.store.PutItem(ctx, inflight); err != nil {
		return fmt.Errorf("persist approval item %s dispatch: %w", item.ID, err)
	}

	if err := responder.RespondApproval(ctx, response); err != nil {
		item.UpdatedAt = m.now()
		_ = m.store.PutItem(ctx, item)
		return err
	}

	finalItem := inflight
	if response.Decision == ApprovalDecisionApproved {
		finalItem.Status = ItemStatusCompleted
	} else {
		finalItem.Status = ItemStatusCancelled
	}
	finalItem.UpdatedAt = m.now()
	if err := m.store.PutItem(ctx, finalItem); err != nil {
		return fmt.Errorf("persist approval item %s response: %w", item.ID, err)
	}

	if !CanTransitionTurnStatus(turn.Status, TurnStatusRunning) {
		return fmt.Errorf("invalid turn status transition %s -> %s", turn.Status, TurnStatusRunning)
	}
	turn.Status = TurnStatusRunning
	turn.UpdatedAt = m.now()
	if err := m.store.PutTurn(ctx, turn); err != nil {
		return fmt.Errorf("persist turn %s approval resume: %w", turn.ID, err)
	}
	return nil
}
