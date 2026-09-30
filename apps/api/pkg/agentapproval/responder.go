package agentapproval

import (
	"context"
	"fmt"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

// ManagerResponder adapts the provider-neutral runtime manager to the worker's
// approval response boundary.
type ManagerResponder struct {
	manager *agentruntime.Manager
}

// NewManagerResponder creates an approval responder backed by a runtime Manager.
func NewManagerResponder(manager *agentruntime.Manager) *ManagerResponder {
	return &ManagerResponder{manager: manager}
}

// RespondAgentApproval sends one approved/denied action back to the provider turn.
func (r *ManagerResponder) RespondAgentApproval(ctx context.Context, threadID, turnID, itemID string, approved bool, note string) error {
	if r == nil || r.manager == nil {
		return fmt.Errorf("agent runtime manager is not configured")
	}
	decision := agentruntime.ApprovalDecisionDenied
	if approved {
		decision = agentruntime.ApprovalDecisionApproved
	}
	return r.manager.RespondApproval(ctx, agentruntime.ApprovalResponse{
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   itemID,
		Decision: decision,
		Note:     note,
	})
}
