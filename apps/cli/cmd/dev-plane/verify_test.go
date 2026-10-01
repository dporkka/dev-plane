package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/verifier"
)

type fakeVerifyRuntime struct {
	candidate string
	config    []byte
	created   []runtimes.CreateRequest
	destroyed []string
	commands  []runtimes.Command
}

func (f *fakeVerifyRuntime) CreateWorkspace(_ context.Context, req runtimes.CreateRequest) (*runtimes.Session, error) {
	f.created = append(f.created, req)
	return &runtimes.Session{ID: "verify-session", Status: "ready", Provider: "fake"}, nil
}

func (f *fakeVerifyRuntime) DestroyWorkspace(_ context.Context, sessionID string) error {
	f.destroyed = append(f.destroyed, sessionID)
	return nil
}

func (f *fakeVerifyRuntime) ReadFile(_ context.Context, _ string, path string) ([]byte, error) {
	if path != "devplane.yaml" {
		return nil, errors.New("unexpected file")
	}
	return append([]byte(nil), f.config...), nil
}

func (f *fakeVerifyRuntime) ExecuteCommand(_ context.Context, _ string, cmd runtimes.Command) (*runtimes.CommandResult, error) {
	f.commands = append(f.commands, cmd)
	if len(cmd.Args) == 3 && cmd.Args[0] == "git" && cmd.Args[1] == "rev-parse" && cmd.Args[2] == "HEAD" {
		return &runtimes.CommandResult{Stdout: f.candidate + "\n", ExitCode: 0}, nil
	}
	if cmd.Command == "make quality" {
		return &runtimes.CommandResult{Stdout: "quality ok\n", ExitCode: 0}, nil
	}
	return nil, errors.New("unexpected command")
}

func TestRunVerifyWithExecutesExactHeadGateAndWritesEvidence(t *testing.T) {
	source, head := initVerifySourceRepo(t)
	evidencePath := filepath.Join(t.TempDir(), "evidence.json")
	runtime := &fakeVerifyRuntime{
		candidate: head,
		config: []byte(`version: 1
verification:
  quality:
    command: make quality
work:
  isolation: container
  max_parallel_cost: 2
review:
  exact_head: true
`),
	}
	factoryCalls := 0
	factory := func(opts verifyOptions) (verifier.WorkspaceRuntime, func() error, error) {
		factoryCalls++
		if opts.Provider != "local" {
			t.Fatalf("provider = %q", opts.Provider)
		}
		return runtime, func() error { return nil }, nil
	}

	var output bytes.Buffer
	err := runVerifyWith(context.Background(), []string{
		"--provider=local",
		"--source=" + source,
		"--repository=dporkka/example",
		"--candidate-sha=" + head,
		"--base-sha=" + head,
		"--gate=quality",
		"--changed-path=src/main.go",
		"--evidence-out=" + evidencePath,
	}, &output, factory)
	if err != nil {
		t.Fatalf("runVerifyWith() error = %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("runtime factory calls = %d, want 1", factoryCalls)
	}
	if len(runtime.created) != 1 {
		t.Fatalf("workspace creates = %d, want 1", len(runtime.created))
	}
	created := runtime.created[0]
	if created.BaseBranch != head || created.Capabilities.Network || len(created.Capabilities.Secrets) != 0 {
		t.Fatalf("workspace request = %#v", created)
	}
	if len(runtime.destroyed) != 1 || runtime.destroyed[0] != "verify-session" {
		t.Fatalf("destroyed workspaces = %#v", runtime.destroyed)
	}

	var result verifyOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode verify output: %v\n%s", err, output.String())
	}
	if result.Status != "passed" || result.Evidence.HeadSHA != head {
		t.Fatalf("verify output = %#v", result)
	}
	if len(result.Evidence.Gates) != 1 || result.Evidence.Gates[0].Name != "quality" || result.Evidence.Gates[0].Status != repoprotocol.GatePassed {
		t.Fatalf("gate evidence = %#v", result.Evidence.Gates)
	}

	evidenceBytes, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("read evidence file: %v", err)
	}
	if !bytes.Contains(evidenceBytes, []byte(head)) || !bytes.Contains(evidenceBytes, []byte(`"quality"`)) {
		t.Fatalf("evidence file missing exact head/gate: %s", evidenceBytes)
	}
}

func TestRunVerifyWithRejectsHostSourceDriftBeforeProvisioning(t *testing.T) {
	source, _ := initVerifySourceRepo(t)
	factoryCalled := false
	factory := func(verifyOptions) (verifier.WorkspaceRuntime, func() error, error) {
		factoryCalled = true
		return nil, nil, errors.New("must not be called")
	}

	err := runVerifyWith(context.Background(), []string{
		"--provider=local",
		"--source=" + source,
		"--repository=dporkka/example",
		"--candidate-sha=" + strings.Repeat("a", 40),
		"--base-sha=" + strings.Repeat("b", 40),
		"--gate=quality",
	}, &bytes.Buffer{}, factory)
	if err == nil || !strings.Contains(err.Error(), "source HEAD mismatch") {
		t.Fatalf("runVerifyWith() error = %v, want source HEAD mismatch", err)
	}
	if factoryCalled {
		t.Fatal("runtime was provisioned before exact source revision validation")
	}
}

func initVerifySourceRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	commands := [][]string{
		{"init"},
		{"config", "user.email", "ci@example.invalid"},
		{"config", "user.name", "CI"},
	}
	for _, args := range commands {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("resolve fixture HEAD: %v", err)
	}
	return dir, strings.TrimSpace(string(output))
}
