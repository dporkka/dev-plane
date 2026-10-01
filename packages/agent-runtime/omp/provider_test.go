package omp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

type fakeClient struct {
	openBinding SessionBinding
	openDir     string
	modelProv   string
	modelID     string
	promptID    string
	promptText  string
	steerText   string
	aborted     bool
	compacted   bool
	frames      chan Frame
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		openBinding: SessionBinding{SessionID: "omp-session-1", SessionFile: "/sessions/thread/session.jsonl"},
		promptID:    "req_1",
		frames:      make(chan Frame, 16),
	}
}

func (f *fakeClient) OpenSession(_ context.Context, dir string) (SessionBinding, error) {
	f.openDir = dir
	return f.openBinding, nil
}
func (f *fakeClient) SetModel(_ context.Context, provider, modelID string) error {
	f.modelProv, f.modelID = provider, modelID
	return nil
}
func (f *fakeClient) Prompt(_ context.Context, message string) (string, error) {
	f.promptText = message
	return f.promptID, nil
}
func (f *fakeClient) Steer(_ context.Context, message string) error {
	f.steerText = message
	return nil
}
func (f *fakeClient) Abort(context.Context) error       { f.aborted = true; return nil }
func (f *fakeClient) Compact(context.Context) error     { f.compacted = true; return nil }
func (f *fakeClient) Subscribe() (<-chan Frame, func()) { return f.frames, func() {} }
func (f *fakeClient) Close() error                      { return nil }

func TestProviderCapabilities(t *testing.T) {
	provider, err := NewProvider(Config{SessionRoot: t.TempDir(), NewClient: func(context.Context, StdioConfig) (RPCClient, error) {
		return newFakeClient(), nil
	}})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	caps := provider.Capabilities()
	for _, cap := range []agentruntime.Capability{
		agentruntime.CapabilityResumeThread,
		agentruntime.CapabilityInterruptTurn,
		agentruntime.CapabilitySteerTurn,
		agentruntime.CapabilityModelSwitch,
		agentruntime.CapabilityCompact,
	} {
		if !caps.Supports(cap) {
			t.Fatalf("capability %s not advertised", cap.String())
		}
	}
	if caps.Supports(agentruntime.CapabilityApprovalRequests) {
		t.Fatal("OMP adapter must not advertise approvals before permission routing is implemented")
	}
}

func TestCreateThreadBindsDedicatedSessionAndModel(t *testing.T) {
	root := t.TempDir()
	client := newFakeClient()
	var gotConfig StdioConfig
	provider, err := NewProvider(Config{
		SessionRoot:          root,
		DefaultModelProvider: "openrouter",
		ResolveWorkspaceDir: func(_ context.Context, workspaceID string) (string, error) {
			if workspaceID != "ws-1" {
				t.Fatalf("workspace id = %q", workspaceID)
			}
			return "/workspaces/ws-1", nil
		},
		NewClient: func(_ context.Context, cfg StdioConfig) (RPCClient, error) {
			gotConfig = cfg
			return client, nil
		},
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	thread, err := provider.CreateThread(context.Background(), agentruntime.CreateThreadRequest{
		WorkspaceID:  "ws-1",
		Model:        "anthropic/claude-sonnet-4-5",
		Instructions: "follow repository policy",
	})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if thread.ID != "omp-session-1" || thread.Provider != "omp" || thread.WorkspaceID != "ws-1" {
		t.Fatalf("thread = %#v", thread)
	}
	if filepath.Dir(thread.ProviderThreadID) != root {
		t.Fatalf("provider thread dir = %q, want child of %q", thread.ProviderThreadID, root)
	}
	if client.openDir != thread.ProviderThreadID {
		t.Fatalf("open session dir = %q, want %q", client.openDir, thread.ProviderThreadID)
	}
	if gotConfig.Cwd != "/workspaces/ws-1" || gotConfig.AppendSystemPrompt != "follow repository policy" {
		t.Fatalf("stdio config = %#v", gotConfig)
	}
	if client.modelProv != "anthropic" || client.modelID != "claude-sonnet-4-5" {
		t.Fatalf("model = %s/%s", client.modelProv, client.modelID)
	}
}

func TestResumeThreadReopensProviderSessionDirectory(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "thread-1")
	client := newFakeClient()
	provider, err := NewProvider(Config{SessionRoot: root, NewClient: func(context.Context, StdioConfig) (RPCClient, error) {
		return client, nil
	}})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	resumed, err := provider.ResumeThread(context.Background(), agentruntime.ResumeThreadRequest{
		ThreadID:         "durable-thread-1",
		ProviderThreadID: sessionDir,
		WorkspaceID:      "ws-1",
	})
	if err != nil {
		t.Fatalf("ResumeThread() error = %v", err)
	}
	if client.openDir != sessionDir {
		t.Fatalf("open session dir = %q, want %q", client.openDir, sessionDir)
	}
	if resumed.ID != "durable-thread-1" || resumed.ProviderThreadID != sessionDir {
		t.Fatalf("resumed thread = %#v", resumed)
	}
}

func TestResumeThreadRejectsSessionDirectoryOutsideRoot(t *testing.T) {
	provider, err := NewProvider(Config{SessionRoot: t.TempDir(), NewClient: func(context.Context, StdioConfig) (RPCClient, error) {
		return newFakeClient(), nil
	}})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	_, err = provider.ResumeThread(context.Background(), agentruntime.ResumeThreadRequest{
		ThreadID:         "durable-thread-1",
		ProviderThreadID: filepath.Join(t.TempDir(), "outside"),
		WorkspaceID:      "ws-1",
	})
	if err == nil {
		t.Fatal("ResumeThread() error = nil, want root escape rejection")
	}
}

func TestRunTurnNormalizesMessageToolAndCompletionEvents(t *testing.T) {
	client := newFakeClient()
	provider, err := NewProvider(Config{SessionRoot: t.TempDir(), NewClient: func(context.Context, StdioConfig) (RPCClient, error) {
		return client, nil
	}})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	thread, err := provider.CreateThread(context.Background(), agentruntime.CreateThreadRequest{WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}

	events, err := provider.RunTurn(context.Background(), agentruntime.RunTurnRequest{
		ThreadID:         thread.ID,
		ProviderThreadID: thread.ProviderThreadID,
		Input:            agentruntime.TurnInput{Text: "run tests"},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	client.frames <- Frame{Type: "message_start", MessageID: "msg-1", Raw: json.RawMessage(`{"type":"message_start","messageId":"msg-1","message":{"role":"assistant"}}`)}
	client.frames <- Frame{Type: "message_end", MessageID: "msg-1", Raw: json.RawMessage(`{"type":"message_end","messageId":"msg-1","message":{"role":"assistant"}}`)}
	client.frames <- Frame{Type: "tool_execution_start", ToolCallID: "tool-1", ToolName: "bash", Raw: json.RawMessage(`{"type":"tool_execution_start","toolCallId":"tool-1","toolName":"bash","args":{"command":"go test ./..."}}`)}
	client.frames <- Frame{Type: "tool_execution_end", ToolCallID: "tool-1", ToolName: "bash", Raw: json.RawMessage(`{"type":"tool_execution_end","toolCallId":"tool-1","toolName":"bash","result":{"exitCode":0}}`)}
	client.frames <- Frame{Type: "prompt_result", ID: "req_1", Status: "completed", SessionSettled: true, Raw: json.RawMessage(`{"type":"prompt_result","id":"req_1","status":"completed","agentInvoked":true,"sessionSettled":true}`)}

	var got []agentruntime.Event
	deadline := time.After(2 * time.Second)
	for len(got) < 6 {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed after %d events", len(got))
			}
			got = append(got, event)
		case <-deadline:
			t.Fatalf("timed out waiting for events: %#v", got)
		}
	}

	if got[0].Type != agentruntime.EventTypeTurnStarted || got[0].TurnID != "req_1" {
		t.Fatalf("turn start = %#v", got[0])
	}
	if got[1].Type != agentruntime.EventTypeItemStarted || got[1].Item == nil || got[1].Item.Type != agentruntime.ItemTypeMessage {
		t.Fatalf("message start = %#v", got[1])
	}
	if got[3].Type != agentruntime.EventTypeItemStarted || got[3].Item == nil || got[3].Item.Type != agentruntime.ItemTypeToolCall || got[3].Item.Name != "bash" {
		t.Fatalf("tool start = %#v", got[3])
	}
	if got[5].Type != agentruntime.EventTypeTurnCompleted || got[5].Status != agentruntime.TurnStatusCompleted {
		t.Fatalf("turn completion = %#v", got[5])
	}
}

func TestControlOperationsUseActiveThreadClient(t *testing.T) {
	client := newFakeClient()
	provider, err := NewProvider(Config{SessionRoot: t.TempDir(), NewClient: func(context.Context, StdioConfig) (RPCClient, error) {
		return client, nil
	}})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	thread, err := provider.CreateThread(context.Background(), agentruntime.CreateThreadRequest{WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}

	if err := provider.SteerTurn(context.Background(), agentruntime.SteerTurnRequest{ThreadID: thread.ID, ProviderThreadID: thread.ProviderThreadID, TurnID: "req_1", Input: agentruntime.TurnInput{Text: "focus on failing test"}}); err != nil {
		t.Fatalf("SteerTurn() error = %v", err)
	}
	if err := provider.InterruptTurn(context.Background(), agentruntime.InterruptTurnRequest{ThreadID: thread.ID, ProviderThreadID: thread.ProviderThreadID, TurnID: "req_1"}); err != nil {
		t.Fatalf("InterruptTurn() error = %v", err)
	}
	if err := provider.CompactThread(context.Background(), thread.ProviderThreadID); err != nil {
		t.Fatalf("CompactThread() error = %v", err)
	}
	if client.steerText != "focus on failing test" || !client.aborted || !client.compacted {
		t.Fatalf("control state = steer %q abort %v compact %v", client.steerText, client.aborted, client.compacted)
	}
}

func TestParseModelReference(t *testing.T) {
	provider, model, err := parseModelRef("anthropic/claude-sonnet-4-5", "openrouter")
	if err != nil || provider != "anthropic" || model != "claude-sonnet-4-5" {
		t.Fatalf("explicit model = %q/%q err=%v", provider, model, err)
	}
	provider, model, err = parseModelRef("claude-sonnet-4-5", "openrouter")
	if err != nil || provider != "openrouter" || model != "claude-sonnet-4-5" {
		t.Fatalf("default provider model = %q/%q err=%v", provider, model, err)
	}
}
