package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientPromptStreamsUpdatesAndHandlesPermission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var updates []Update
	client, err := Start(ctx, AgentSpec{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestACPHelperProcess", "--"},
		Env:     []string{"GO_WANT_ACP_HELPER=1"},
	}, Options{
		OnUpdate: func(update Update) { updates = append(updates, update) },
		DecidePermission: func(req PermissionRequest) PermissionDecision {
			if req.ToolCall.Title != "Run tests" {
				t.Fatalf("unexpected title: %q", req.ToolCall.Title)
			}
			return PermissionDecision{OptionID: "allow-once"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	init, err := client.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != 1 {
		t.Fatalf("protocol=%d", init.ProtocolVersion)
	}
	if !init.LoadSession {
		t.Fatal("expected loadSession capability")
	}
	if len(init.AuthMethods) != 1 || init.AuthMethods[0].ID != "chat-gpt" {
		t.Fatalf("authMethods=%+v", init.AuthMethods)
	}
	if err := client.Authenticate(ctx, "chat-gpt"); err != nil {
		t.Fatal(err)
	}

	session, err := client.NewSession(ctx, "/tmp/project", SessionMetadata{TaskID: "task-1", RunID: "run-2"})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "sess-1" {
		t.Fatalf("session=%q", session.ID)
	}

	result, err := client.Prompt(ctx, session.ID, "fix it", SessionMetadata{TaskID: "task-1", RunID: "run-2"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "end_turn" {
		t.Fatalf("stopReason=%q", result.StopReason)
	}

	var text strings.Builder
	for _, update := range updates {
		if update.Kind == "agent_message_chunk" {
			text.WriteString(update.Text)
		}
	}
	if got := text.String(); got != "hello world" {
		t.Fatalf("text=%q", got)
	}
}

func TestLoadSessionRequiresCapability(t *testing.T) {
	c := &Client{init: InitializeResult{ProtocolVersion: 1, LoadSession: false}}
	err := c.LoadSession(context.Background(), "sess-x", "/tmp/project", SessionMetadata{})
	if err == nil || !strings.Contains(err.Error(), "loadSession") {
		t.Fatalf("err=%v", err)
	}
}

func TestChoosePermission(t *testing.T) {
	options := []PermissionOption{
		{OptionID: "yes-always", Kind: "allow_always"},
		{OptionID: "no", Kind: "reject_once"},
		{OptionID: "yes", Kind: "allow_once"},
	}
	if got := ChoosePermission(PermissionAllow, options); got.OptionID != "yes" {
		t.Fatalf("allow=%q", got.OptionID)
	}
	if got := ChoosePermission(PermissionDeny, options); got.OptionID != "no" {
		t.Fatalf("deny=%q", got.OptionID)
	}
}

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ACP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	var promptID any
	for scanner.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			os.Exit(3)
		}
		method, _ := msg["method"].(string)
		id := msg["id"]
		switch method {
		case "initialize":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"protocolVersion":   1,
				"agentCapabilities": map[string]any{"loadSession": true},
				"agentInfo":         map[string]any{"name": "fake-agent", "version": "1"},
				"authMethods":       []map[string]any{{"id": "chat-gpt", "name": "ChatGPT", "description": "Sign in", "type": "agent"}},
			}})
		case "authenticate":
			params := msg["params"].(map[string]any)
			if params["methodId"] != "chat-gpt" {
				os.Exit(6)
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "session/new":
			params := msg["params"].(map[string]any)
			meta := params["_meta"].(map[string]any)["devPlane"].(map[string]any)
			if meta["taskId"] != "task-1" || meta["runId"] != "run-2" {
				os.Exit(4)
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "sess-1"}})
		case "session/load":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "session/prompt":
			promptID = id
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": "sess-1", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "hello "}},
			}})
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "session/request_permission", "params": map[string]any{
				"sessionId": "sess-1",
				"toolCall":  map[string]any{"toolCallId": "tool-1", "title": "Run tests", "kind": "execute", "status": "pending"},
				"options":   []map[string]any{{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"}, {"optionId": "reject", "name": "Reject", "kind": "reject_once"}},
			}})
		default:
			if idFloat, ok := id.(float64); ok && int(idFloat) == 99 {
				result := msg["result"].(map[string]any)
				outcome := result["outcome"].(map[string]any)
				if outcome["optionId"] != "allow-once" {
					fmt.Fprintln(os.Stderr, "wrong permission")
					os.Exit(5)
				}
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
					"sessionId": "sess-1", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "world"}},
				}})
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]any{"stopReason": "end_turn"}})
			}
		}
	}
	os.Exit(0)
}

func TestAuthenticateRejectsTerminalMethod(t *testing.T) {
	c := &Client{init: InitializeResult{AuthMethods: []AuthMethod{{ID: "terminal-login", Type: "terminal"}}}}
	err := c.Authenticate(context.Background(), "terminal-login")
	if err == nil || !strings.Contains(err.Error(), "terminal authentication") {
		t.Fatalf("err=%v", err)
	}
}
