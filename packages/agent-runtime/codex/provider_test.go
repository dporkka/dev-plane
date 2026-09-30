package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

type fakeRPC struct {
	mu            sync.Mutex
	calls         []rpcCall
	responses     map[string]json.RawMessage
	notifications chan Notification
}

type rpcCall struct {
	method string
	params map[string]any
}

func newFakeRPC() *fakeRPC {
	return &fakeRPC{
		responses:     make(map[string]json.RawMessage),
		notifications: make(chan Notification, 16),
	}
}

func (f *fakeRPC) Call(_ context.Context, method string, params any, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}

	f.mu.Lock()
	f.calls = append(f.calls, rpcCall{method: method, params: decoded})
	response, ok := f.responses[method]
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("unexpected method %s", method)
	}
	return json.Unmarshal(response, result)
}

func (f *fakeRPC) Notify(_ context.Context, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var decoded map[string]any
	if raw != nil && string(raw) != "null" {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, rpcCall{method: method, params: decoded})
	f.mu.Unlock()
	return nil
}

func (f *fakeRPC) Subscribe() (<-chan Notification, func()) {
	return f.notifications, func() {}
}

func (f *fakeRPC) Close() error { return nil }

func (f *fakeRPC) call(method string) (rpcCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.method == method {
			return call, true
		}
	}
	return rpcCall{}, false
}

func TestProviderDefaultsFailClosedUntilApprovalRoutingExists(t *testing.T) {
	provider := NewProvider(newFakeRPC(), Config{})

	if provider.config.ApprovalPolicy != "on-request" {
		t.Fatalf("default approval policy = %q, want on-request", provider.config.ApprovalPolicy)
	}
	if provider.config.Sandbox != "read-only" {
		t.Fatalf("default sandbox = %q, want read-only", provider.config.Sandbox)
	}
}

func TestProviderCapabilities(t *testing.T) {
	provider := NewProvider(newFakeRPC(), Config{})

	caps := provider.Capabilities()
	for _, capability := range []agentruntime.Capability{
		agentruntime.CapabilityResumeThread,
		agentruntime.CapabilityInterruptTurn,
		agentruntime.CapabilitySteerTurn,
		agentruntime.CapabilityModelSwitch,
		agentruntime.CapabilityCompact,
		agentruntime.CapabilityStructuredOutput,
	} {
		if !caps.Supports(capability) {
			t.Fatalf("capability %s should be supported", capability.String())
		}
	}
	if caps.Supports(agentruntime.CapabilityApprovalRequests) {
		t.Fatal("approval requests should not be advertised until server-request routing is implemented")
	}
}

func TestCreateThreadMapsWorkspaceAndCodexOptions(t *testing.T) {
	rpc := newFakeRPC()
	rpc.responses["thread/start"] = json.RawMessage(`{
		"thread": {
			"id": "0199-codex-thread",
			"modelProvider": "openai",
			"model": "gpt-5.6-codex",
			"createdAt": 1790784000,
			"updatedAt": 1790784000,
			"status": {"type": "idle"},
			"turns": []
		},
		"model": "gpt-5.6-codex",
		"modelProvider": "openai"
	}`)

	provider := NewProvider(rpc, Config{
		ApprovalPolicy: "never",
		Sandbox:        "workspace-write",
		ResolveWorkspaceDir: func(_ context.Context, workspaceID string) (string, error) {
			if workspaceID != "workspace-1" {
				return "", fmt.Errorf("unexpected workspace %s", workspaceID)
			}
			return "/worktrees/workspace-1", nil
		},
	})

	thread, err := provider.CreateThread(context.Background(), agentruntime.CreateThreadRequest{
		WorkspaceID:  "workspace-1",
		Model:        "gpt-5.6-codex",
		Instructions: "Use repository conventions.",
	})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if thread.ID != "0199-codex-thread" || thread.ProviderThreadID != "0199-codex-thread" {
		t.Fatalf("thread ids = %q/%q", thread.ID, thread.ProviderThreadID)
	}
	if thread.WorkspaceID != "workspace-1" || thread.Provider != "codex" {
		t.Fatalf("thread ownership = %#v", thread)
	}
	if thread.Model != "gpt-5.6-codex" || thread.Status != agentruntime.ThreadStatusActive {
		t.Fatalf("thread state = %#v", thread)
	}

	call, ok := rpc.call("thread/start")
	if !ok {
		t.Fatal("thread/start was not called")
	}
	want := map[string]any{
		"model":                "gpt-5.6-codex",
		"cwd":                  "/worktrees/workspace-1",
		"approvalPolicy":       "never",
		"sandbox":              "workspace-write",
		"developerInstructions": "Use repository conventions.",
	}
	for key, value := range want {
		if !reflect.DeepEqual(call.params[key], value) {
			t.Fatalf("thread/start %s = %#v, want %#v", key, call.params[key], value)
		}
	}
}

func TestResumeThreadPreservesDurableWorkspaceOwnership(t *testing.T) {
	rpc := newFakeRPC()
	rpc.responses["thread/resume"] = json.RawMessage(`{
		"thread": {
			"id": "0199-codex-thread",
			"modelProvider": "openai",
			"model": "gpt-5.6-codex",
			"createdAt": 1790784000,
			"updatedAt": 1790784010,
			"status": {"type": "idle"},
			"turns": []
		},
		"model": "gpt-5.6-codex",
		"modelProvider": "openai"
	}`)

	provider := NewProvider(rpc, Config{})
	thread, err := provider.ResumeThread(context.Background(), agentruntime.ResumeThreadRequest{
		ThreadID:         "0199-codex-thread",
		ProviderThreadID: "0199-codex-thread",
		WorkspaceID:      "workspace-1",
	})
	if err != nil {
		t.Fatalf("ResumeThread() error = %v", err)
	}
	if thread.WorkspaceID != "workspace-1" {
		t.Fatalf("WorkspaceID = %q, want workspace-1", thread.WorkspaceID)
	}

	call, ok := rpc.call("thread/resume")
	if !ok {
		t.Fatal("thread/resume was not called")
	}
	if got := call.params["threadId"]; got != "0199-codex-thread" {
		t.Fatalf("threadId = %#v", got)
	}
	if got := call.params["excludeTurns"]; got != true {
		t.Fatalf("excludeTurns = %#v, want true", got)
	}
}

func TestRunTurnStreamsProviderNeutralEvents(t *testing.T) {
	rpc := newFakeRPC()
	rpc.responses["turn/start"] = json.RawMessage(`{
		"turn": {
			"id": "turn-1",
			"items": [],
			"itemsView": {"type": "full"},
			"status": "inProgress",
			"error": null,
			"startedAt": 1790784000,
			"completedAt": null,
			"durationMs": null
		}
	}`)
	rpc.notifications <- Notification{
		Method: "item/started",
		Params: json.RawMessage(`{
			"threadId":"0199-codex-thread",
			"turnId":"turn-1",
			"startedAtMs":1790784000001,
			"item":{"type":"commandExecution","id":"item-1","command":"go test ./..."}
		}`),
	}
	rpc.notifications <- Notification{
		Method: "item/completed",
		Params: json.RawMessage(`{
			"threadId":"0199-codex-thread",
			"turnId":"turn-1",
			"completedAtMs":1790784000100,
			"item":{"type":"commandExecution","id":"item-1","command":"go test ./...","status":"completed"}
		}`),
	}
	rpc.notifications <- Notification{
		Method: "turn/completed",
		Params: json.RawMessage(`{
			"threadId":"0199-codex-thread",
			"turn":{"id":"turn-1","items":[],"itemsView":{"type":"full"},"status":"completed","error":null,"startedAt":1790784000,"completedAt":1790784001,"durationMs":1000}
		}`),
	}

	provider := NewProvider(rpc, Config{})
	events, err := provider.RunTurn(context.Background(), agentruntime.RunTurnRequest{
		ThreadID:     "0199-codex-thread",
		Input:        agentruntime.TurnInput{Text: "Run the tests"},
		Model:        "gpt-5.6-codex",
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	var got []agentruntime.Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 4 {
		t.Fatalf("event count = %d, want 4: %#v", len(got), got)
	}
	if got[0].Type != agentruntime.EventTypeTurnStarted || got[0].TurnID != "turn-1" {
		t.Fatalf("first event = %#v", got[0])
	}
	if got[1].Type != agentruntime.EventTypeItemStarted || got[1].Item == nil || got[1].Item.Type != agentruntime.ItemTypeCommand {
		t.Fatalf("item started = %#v", got[1])
	}
	if got[2].Type != agentruntime.EventTypeItemCompleted || got[2].Item == nil || got[2].Item.Status != agentruntime.ItemStatusCompleted {
		t.Fatalf("item completed = %#v", got[2])
	}
	if got[3].Type != agentruntime.EventTypeTurnCompleted || got[3].Status != agentruntime.TurnStatusCompleted {
		t.Fatalf("turn completed = %#v", got[3])
	}
	for i, event := range got {
		if event.Sequence != int64(i+1) {
			t.Fatalf("event %d sequence = %d, want %d", i, event.Sequence, i+1)
		}
	}

	call, ok := rpc.call("turn/start")
	if !ok {
		t.Fatal("turn/start was not called")
	}
	if got := call.params["threadId"]; got != "0199-codex-thread" {
		t.Fatalf("turn/start threadId = %#v", got)
	}
	input, ok := call.params["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("turn/start input = %#v", call.params["input"])
	}
	textInput, ok := input[0].(map[string]any)
	if !ok || textInput["type"] != "text" || textInput["text"] != "Run the tests" {
		t.Fatalf("turn/start text input = %#v", input[0])
	}
	if call.params["model"] != "gpt-5.6-codex" {
		t.Fatalf("turn/start model = %#v", call.params["model"])
	}
	if _, ok := call.params["outputSchema"].(map[string]any); !ok {
		t.Fatalf("turn/start outputSchema = %#v", call.params["outputSchema"])
	}
}

func TestInterruptSteerAndCompactMapToCodexMethods(t *testing.T) {
	rpc := newFakeRPC()
	rpc.responses["turn/interrupt"] = json.RawMessage(`{}`)
	rpc.responses["turn/steer"] = json.RawMessage(`{}`)
	rpc.responses["thread/compact/start"] = json.RawMessage(`{}`)
	provider := NewProvider(rpc, Config{})

	if err := provider.InterruptTurn(context.Background(), agentruntime.InterruptTurnRequest{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
	}); err != nil {
		t.Fatalf("InterruptTurn() error = %v", err)
	}
	if err := provider.SteerTurn(context.Background(), agentruntime.SteerTurnRequest{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Input:    agentruntime.TurnInput{Text: "Use the focused test instead"},
	}); err != nil {
		t.Fatalf("SteerTurn() error = %v", err)
	}
	if err := provider.CompactThread(context.Background(), "thread-1"); err != nil {
		t.Fatalf("CompactThread() error = %v", err)
	}

	steer, ok := rpc.call("turn/steer")
	if !ok {
		t.Fatal("turn/steer was not called")
	}
	if steer.params["expectedTurnId"] != "turn-1" {
		t.Fatalf("expectedTurnId = %#v", steer.params["expectedTurnId"])
	}
}


func TestSendEventReturnsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := make(chan agentruntime.Event)
	if sendEvent(ctx, out, agentruntime.Event{}) {
		t.Fatal("sendEvent() = true, want false for cancelled context")
	}
}

func TestProviderRejectsInvalidRawInput(t *testing.T) {
	rpc := newFakeRPC()
	provider := NewProvider(rpc, Config{})

	_, err := provider.RunTurn(context.Background(), agentruntime.RunTurnRequest{
		ThreadID: "thread-1",
		Input:     agentruntime.TurnInput{Data: json.RawMessage(`{"type":"text"}`)},
	})
	if err == nil {
		t.Fatal("RunTurn() error = nil, want raw input array validation error")
	}
}
