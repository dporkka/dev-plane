package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ai-dev-control-plane/cli/internal/acp"
)

func TestResolveAgentDefaults(t *testing.T) {
	spec, err := resolveAgent("codex", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command != "npx" {
		t.Fatalf("command=%q", spec.Command)
	}
	if got := spec.Args; len(got) != 2 || got[1] != "@agentclientprotocol/codex-acp" {
		t.Fatalf("args=%v", got)
	}

	spec, err = resolveAgent("gemini", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command != "gemini" || len(spec.Args) != 1 || spec.Args[0] != "--acp" {
		t.Fatalf("spec=%+v", spec)
	}
}

func TestInteractivePermissionDefaultsToRejectOnBlank(t *testing.T) {
	in := bytes.NewBufferString("\n")
	out := &bytes.Buffer{}
	decide := permissionDecider(acp.PermissionAsk, in, out)
	got := decide(acp.PermissionRequest{ToolCall: acp.ToolCall{Title: "Run rm"}, Options: []acp.PermissionOption{
		{OptionID: "allow", Name: "Allow once", Kind: "allow_once"},
		{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
	}})
	if got.OptionID != "reject" {
		t.Fatalf("decision=%+v output=%q", got, out.String())
	}
}

func TestSaveSessionState(t *testing.T) {
	dir := t.TempDir()
	path, err := saveSessionState(dir, sessionState{Agent: "codex", SessionID: "sess-1", CWD: "/tmp/project", TaskID: "task-1", RunID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got sessionState
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "sess-1" || got.TaskID != "task-1" {
		t.Fatalf("state=%+v", got)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("path=%s", path)
	}
}
