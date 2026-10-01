package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

const (
	commandApprovalMethod = "item/commandExecution/requestApproval"
	fileApprovalMethod    = "item/fileChange/requestApproval"
)

// ServerRequest is a client-directed JSON-RPC request emitted by Codex app-server.
// Unlike notifications, requests must receive an explicit response before Codex can continue.
type ServerRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// ServerRequestClient is implemented by transports that can surface and answer
// Codex client-directed requests.
type ServerRequestClient interface {
	SubscribeRequests() (<-chan ServerRequest, func())
	Respond(ctx context.Context, id json.RawMessage, result any) error
}

type approvalRequestStore struct {
	values sync.Map
}

func (s *approvalRequestStore) Store(itemID string, requestID json.RawMessage) {
	if strings.TrimSpace(itemID) == "" || len(requestID) == 0 {
		return
	}
	s.values.Store(itemID, append(json.RawMessage(nil), requestID...))
}

func (s *approvalRequestStore) LoadAndDelete(itemID string) (json.RawMessage, bool) {
	value, ok := s.values.LoadAndDelete(itemID)
	if !ok {
		return nil, false
	}
	requestID, ok := value.(json.RawMessage)
	return requestID, ok
}

func supportsServerRequest(method string) bool {
	switch method {
	case commandApprovalMethod, fileApprovalMethod:
		return true
	default:
		return false
	}
}

func mapServerRequest(
	request ServerRequest,
	threadID string,
	providerThreadID string,
	turnID string,
) (agentruntime.Event, bool, error) {
	if !supportsServerRequest(request.Method) {
		return agentruntime.Event{}, false, fmt.Errorf("unsupported Codex server request %q", request.Method)
	}

	var raw map[string]any
	if err := json.Unmarshal(request.Params, &raw); err != nil {
		return agentruntime.Event{}, false, fmt.Errorf("decode Codex approval request: %w", err)
	}
	nativeThreadID, _ := raw["threadId"].(string)
	nativeTurnID, _ := raw["turnId"].(string)
	itemID, _ := raw["itemId"].(string)
	if nativeThreadID != providerThreadID || nativeTurnID != turnID {
		return agentruntime.Event{}, false, nil
	}
	if strings.TrimSpace(itemID) == "" {
		return agentruntime.Event{}, false, fmt.Errorf("Codex approval request is missing itemId")
	}

	now := time.Now().UTC()
	item := agentruntime.Item{
		ID:             itemID,
		ThreadID:       threadID,
		TurnID:         turnID,
		ProviderItemID: itemID,
		Type:           agentruntime.ItemTypeApproval,
		Status:         agentruntime.ItemStatusPending,
		Name:           request.Method,
		Payload:        append(json.RawMessage(nil), request.Params...),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return agentruntime.Event{
		Type:           agentruntime.EventTypeItemStarted,
		ThreadID:       threadID,
		TurnID:         turnID,
		ProviderTurnID: turnID,
		Status:         agentruntime.TurnStatusPausedApproval,
		Item:           &item,
		OccurredAt:     now,
	}, true, nil
}

// RespondApproval resolves one pending Codex command/file-change approval.
// The generic bridge grants only the single requested action.
func (p *Provider) RespondApproval(ctx context.Context, response agentruntime.ApprovalResponse) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("codex client is not configured")
	}
	interactive, ok := p.client.(ServerRequestClient)
	if !ok {
		return fmt.Errorf("codex transport does not support approval responses")
	}
	requestID, ok := p.pendingApprovals.LoadAndDelete(response.ItemID)
	if !ok {
		return fmt.Errorf("no pending Codex approval request for item %q", response.ItemID)
	}

	decision := "decline"
	switch response.Decision {
	case agentruntime.ApprovalDecisionApproved:
		decision = "accept"
	case agentruntime.ApprovalDecisionDenied:
		decision = "decline"
	default:
		p.pendingApprovals.Store(response.ItemID, requestID)
		return fmt.Errorf("unsupported approval decision %q", response.Decision)
	}
	if err := interactive.Respond(ctx, requestID, map[string]any{"decision": decision}); err != nil {
		p.pendingApprovals.Store(response.ItemID, requestID)
		return fmt.Errorf("respond to Codex approval %s: %w", response.ItemID, err)
	}
	return nil
}

var _ agentruntime.ApprovalProvider = (*Provider)(nil)
