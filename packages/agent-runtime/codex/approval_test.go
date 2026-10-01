package codex

import (
	"context"
	"encoding/json"
	"testing"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

type fakeServerRequestRPC struct {
	*fakeRPC
	requests  chan ServerRequest
	responses []serverResponse
}

type serverResponse struct {
	id     json.RawMessage
	result any
}

func newFakeServerRequestRPC() *fakeServerRequestRPC {
	return &fakeServerRequestRPC{
		fakeRPC:  newFakeRPC(),
		requests: make(chan ServerRequest, 4),
	}
}

func (f *fakeServerRequestRPC) SubscribeRequests() (<-chan ServerRequest, func()) {
	return f.requests, func() {}
}

func (f *fakeServerRequestRPC) Respond(_ context.Context, id json.RawMessage, result any) error {
	f.responses = append(f.responses, serverResponse{id: append(json.RawMessage(nil), id...), result: result})
	return nil
}

func TestApprovalCapAdvertisedOnlyWhenTransportSupportsServerRequests(t *testing.T) {
	plain := NewProvider(newFakeRPC(), Config{})
	if plain.Capabilities().Supports(agentruntime.CapabilityApprovalRequests) {
		t.Fatal("plain RPC transport must not advertise approval requests")
	}

	interactive := NewProvider(newFakeServerRequestRPC(), Config{})
	if !interactive.Capabilities().Supports(agentruntime.CapabilityApprovalRequests) {
		t.Fatal("interactive transport should advertise approval requests")
	}
}

func TestMapCommandApprovalRequestToPausedApprovalItem(t *testing.T) {
	request := ServerRequest{
		ID:     json.RawMessage(`17`),
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{
			"threadId":"native-thread",
			"turnId":"native-turn",
			"itemId":"command-1",
			"command":"go test ./...",
			"cwd":"/workspace",
			"reason":"requires workspace execution"
		}`),
	}

	event, matches, err := mapServerRequest(request, "thread-1", "native-thread", "native-turn")
	if err != nil {
		t.Fatalf("mapServerRequest() error = %v", err)
	}
	if !matches {
		t.Fatal("mapServerRequest() matches = false, want true")
	}
	if event.Type != agentruntime.EventTypeItemStarted || event.Status != agentruntime.TurnStatusPausedApproval {
		t.Fatalf("event = %#v", event)
	}
	if event.Item == nil || event.Item.ID != "command-1" || event.Item.Type != agentruntime.ItemTypeApproval {
		t.Fatalf("approval item = %#v", event.Item)
	}
}

func TestRespondApprovalMapsDecisionBackToCodex(t *testing.T) {
	rpc := newFakeServerRequestRPC()
	provider := NewProvider(rpc, Config{})
	provider.pendingApprovals.Store("command-1", json.RawMessage(`17`))

	err := provider.RespondApproval(context.Background(), agentruntime.ApprovalResponse{
		ThreadID: "thread-1",
		TurnID:   "native-turn",
		ItemID:   "command-1",
		Decision: agentruntime.ApprovalDecisionApproved,
	})
	if err != nil {
		t.Fatalf("RespondApproval() error = %v", err)
	}
	if len(rpc.responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(rpc.responses))
	}
	payload, ok := rpc.responses[0].result.(map[string]any)
	if !ok || payload["decision"] != "accept" {
		t.Fatalf("response payload = %#v", rpc.responses[0].result)
	}
}

func TestUnsupportedServerRequestFailsClosed(t *testing.T) {
	request := ServerRequest{
		ID:     json.RawMessage(`18`),
		Method: "item/permissions/requestApproval",
		Params: json.RawMessage(`{"threadId":"native-thread","turnId":"native-turn","itemId":"permission-1"}`),
	}
	_, matches, err := mapServerRequest(request, "thread-1", "native-thread", "native-turn")
	if err == nil {
		t.Fatal("mapServerRequest() error = nil, want unsupported request error")
	}
	if matches {
		t.Fatal("unsupported request must not be treated as handled")
	}
}
