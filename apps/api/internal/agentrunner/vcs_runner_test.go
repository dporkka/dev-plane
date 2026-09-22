package agentrunner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/runtimes"
)

type vcsRuntimeTestProvider struct {
	commands []runtimes.Command
}

func (p *vcsRuntimeTestProvider) CreateWorkspace(context.Context, runtimes.CreateRequest) (*runtimes.Session, error) {
	return nil, nil
}

func (p *vcsRuntimeTestProvider) DestroyWorkspace(context.Context, string) error {
	return nil
}

func (p *vcsRuntimeTestProvider) ExecuteCommand(_ context.Context, _ string, cmd runtimes.Command) (*runtimes.CommandResult, error) {
	p.commands = append(p.commands, cmd)
	switch {
	case strings.Contains(cmd.Command, "'diff' '--cached' '--name-only'"):
		return &runtimes.CommandResult{Stdout: "changed.go\n"}, nil
	case strings.Contains(cmd.Command, "'rev-parse' 'HEAD'"):
		return &runtimes.CommandResult{Stdout: "deadbeef\n"}, nil
	default:
		return &runtimes.CommandResult{}, nil
	}
}

func (p *vcsRuntimeTestProvider) ReadFile(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func (p *vcsRuntimeTestProvider) WriteFile(context.Context, string, string, []byte) error {
	return nil
}

func (p *vcsRuntimeTestProvider) ApplyPatch(context.Context, string, string) error {
	return nil
}

func (p *vcsRuntimeTestProvider) Snapshot(context.Context, string) (*runtimes.Snapshot, error) {
	return nil, nil
}

func (p *vcsRuntimeTestProvider) Restore(context.Context, string, *runtimes.Snapshot) error {
	return nil
}

func (p *vcsRuntimeTestProvider) GetStatus(context.Context, string) (*runtimes.SessionStatus, error) {
	return nil, nil
}

func (p *vcsRuntimeTestProvider) StreamLogs(context.Context, string) (<-chan runtimes.LogLine, error) {
	return nil, nil
}

func TestRuntimeCreateCommitUsesSharedVCSBackend(t *testing.T) {
	provider := &vcsRuntimeTestProvider{}
	input := json.RawMessage(`{"message":"feat(parser): handle user's input"}`)

	raw, err := runtimeCreateCommit(context.Background(), provider, "session-1", input)
	if err != nil {
		t.Fatalf("runtimeCreateCommit() error = %v", err)
	}

	var result struct {
		Success    bool   `json:"success"`
		CommitHash string `json:"commit_hash"`
		ChangeID   string `json:"change_id"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Success || result.Error != "" {
		t.Fatalf("result = %+v", result)
	}
	if result.CommitHash != "deadbeef" || result.ChangeID != "deadbeef" {
		t.Fatalf("revision = (%q, %q), want deadbeef", result.CommitHash, result.ChangeID)
	}
	if len(provider.commands) != 4 {
		t.Fatalf("command count = %d, want 4: %#v", len(provider.commands), provider.commands)
	}

	commitSeen := false
	for _, cmd := range provider.commands {
		if !cmd.UnsafeShell {
			t.Fatalf("typed VCS transport command must be marked trusted system shell: %#v", cmd)
		}
		if cmd.Env["GIT_TERMINAL_PROMPT"] != "0" {
			t.Fatalf("GIT_TERMINAL_PROMPT = %q, want 0", cmd.Env["GIT_TERMINAL_PROMPT"])
		}
		if cmd.Env["GIT_AUTHOR_EMAIL"] != "dev-plane@example.invalid" ||
			cmd.Env["GIT_COMMITTER_EMAIL"] != "dev-plane@example.invalid" {
			t.Fatalf("git identity not set: %#v", cmd.Env)
		}
		if strings.Contains(cmd.Command, "'commit'") {
			commitSeen = true
			if !strings.Contains(cmd.Command, "feat(parser)") {
				t.Fatalf("commit command lost message: %q", cmd.Command)
			}
		}
	}
	if !commitSeen {
		t.Fatal("shared VCS backend did not issue a commit command")
	}
}
