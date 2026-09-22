package prfactory

import (
	"context"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/runtimes"
)

type publishRuntimeProvider struct {
	attachedSession string
	attachedWS      string
	commands        []runtimes.Command
}

func (p *publishRuntimeProvider) CreateWorkspace(context.Context, runtimes.CreateRequest) (*runtimes.Session, error) {
	return nil, nil
}

func (p *publishRuntimeProvider) DestroyWorkspace(context.Context, string) error {
	return nil
}

func (p *publishRuntimeProvider) ExecuteCommand(_ context.Context, _ string, cmd runtimes.Command) (*runtimes.CommandResult, error) {
	p.commands = append(p.commands, cmd)
	return &runtimes.CommandResult{ExitCode: 0}, nil
}

func (p *publishRuntimeProvider) ReadFile(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func (p *publishRuntimeProvider) WriteFile(context.Context, string, string, []byte) error {
	return nil
}

func (p *publishRuntimeProvider) ApplyPatch(context.Context, string, string) error {
	return nil
}

func (p *publishRuntimeProvider) Snapshot(context.Context, string) (*runtimes.Snapshot, error) {
	return nil, nil
}

func (p *publishRuntimeProvider) Restore(context.Context, string, *runtimes.Snapshot) error {
	return nil
}

func (p *publishRuntimeProvider) GetStatus(context.Context, string) (*runtimes.SessionStatus, error) {
	return nil, nil
}

func (p *publishRuntimeProvider) StreamLogs(context.Context, string) (<-chan runtimes.LogLine, error) {
	return nil, nil
}

func (p *publishRuntimeProvider) AttachSession(_ context.Context, sessionID, workspaceID string) (*runtimes.Session, error) {
	p.attachedSession = sessionID
	p.attachedWS = workspaceID
	return &runtimes.Session{ID: sessionID, WorkspaceID: workspaceID, Status: "ready", Provider: "docker"}, nil
}

func TestPublishWorkspaceBranchUsesRuntimeVCSBackend(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	provider := &publishRuntimeProvider{}
	factory := NewFactory(nil, nil).
		WithGitHubToken("ghp_runtime_secret").
		WithRuntimeProvider("docker", provider)

	sessionID := "session-1"
	workspace := &models.Workspace{
		ID:               "workspace-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
	}

	if err := factory.publishWorkspaceBranch(context.Background(), workspace, "agent/task-1"); err != nil {
		t.Fatalf("publishWorkspaceBranch() error = %v", err)
	}
	if provider.attachedSession != sessionID || provider.attachedWS != workspace.ID {
		t.Fatalf("attached = %q/%q, want %q/%q", provider.attachedSession, provider.attachedWS, sessionID, workspace.ID)
	}
	if len(provider.commands) != 1 {
		t.Fatalf("commands = %d, want 1: %#v", len(provider.commands), provider.commands)
	}

	cmd := provider.commands[0]
	if cmd.Dir != "." {
		t.Fatalf("command dir = %q, want .", cmd.Dir)
	}
	if !strings.Contains(cmd.Command, "'git' 'push' '-u' 'origin' 'agent/task-1'") {
		t.Fatalf("command = %q, want shared git publish", cmd.Command)
	}
	if strings.Contains(cmd.Command, "ghp_runtime_secret") {
		t.Fatalf("token leaked into command: %q", cmd.Command)
	}
	if cmd.Env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Fatalf("GIT_TERMINAL_PROMPT = %q, want 0", cmd.Env["GIT_TERMINAL_PROMPT"])
	}
	if cmd.Env["GIT_CONFIG_KEY_0"] != "http.extraHeader" {
		t.Fatalf("GIT_CONFIG_KEY_0 = %q", cmd.Env["GIT_CONFIG_KEY_0"])
	}
	if !strings.HasPrefix(cmd.Env["GIT_CONFIG_VALUE_0"], "Authorization: Basic ") {
		t.Fatalf("GIT_CONFIG_VALUE_0 = %q", cmd.Env["GIT_CONFIG_VALUE_0"])
	}
}

func TestPublishWorkspaceBranchRejectsUnregisteredRuntime(t *testing.T) {
	sessionID := "session-1"
	factory := NewFactory(nil, nil)
	err := factory.publishWorkspaceBranch(context.Background(), &models.Workspace{
		ID:               "workspace-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
	}, "agent/task-1")
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("error = %v, want unregistered runtime error", err)
	}
}
