package prfactory

import (
	"context"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/vcs"
)

type publishRuntimeProvider struct {
	attachedSession string
	attachedWS      string
	commands        []runtimes.Command
	publishSession  string
	publishReq      runtimes.VCSPublishRequest
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

func (p *publishRuntimeProvider) PublishVCS(_ context.Context, sessionID string, req runtimes.VCSPublishRequest) error {
	p.publishSession = sessionID
	p.publishReq = req
	return nil
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

	if err := factory.publishWorkspaceBranch(context.Background(), workspace, "agent/task-1", "https://github.com/acme/app.git"); err != nil {
		t.Fatalf("publishWorkspaceBranch() error = %v", err)
	}
	if provider.attachedSession != sessionID || provider.attachedWS != workspace.ID {
		t.Fatalf("attached = %q/%q, want %q/%q", provider.attachedSession, provider.attachedWS, sessionID, workspace.ID)
	}
	if len(provider.commands) != 0 {
		t.Fatalf("agent command path used for privileged publication: %#v", provider.commands)
	}
	if provider.publishSession != sessionID {
		t.Fatalf("publish session = %q, want %q", provider.publishSession, sessionID)
	}
	if provider.publishReq.Ref != "agent/task-1" {
		t.Fatalf("publish ref = %q", provider.publishReq.Ref)
	}
	if provider.publishReq.RemoteURL != "https://github.com/acme/app.git" {
		t.Fatalf("publish remote = %q", provider.publishReq.RemoteURL)
	}
	if provider.publishReq.Env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Fatalf("GIT_TERMINAL_PROMPT = %q", provider.publishReq.Env["GIT_TERMINAL_PROMPT"])
	}
	if provider.publishReq.Env["GIT_CONFIG_KEY_0"] != "http.https://github.com/.extraHeader" {
		t.Fatalf("GIT_CONFIG_KEY_0 = %q", provider.publishReq.Env["GIT_CONFIG_KEY_0"])
	}
	if !strings.HasPrefix(provider.publishReq.Env["GIT_CONFIG_VALUE_0"], "Authorization: Basic ") {
		t.Fatalf("GIT_CONFIG_VALUE_0 = %q", provider.publishReq.Env["GIT_CONFIG_VALUE_0"])
	}
}

func TestPublishWorkspaceBranchRejectsUnregisteredRuntime(t *testing.T) {
	sessionID := "session-1"
	factory := NewFactory(nil, nil)
	err := factory.publishWorkspaceBranch(context.Background(), &models.Workspace{
		ID:               "workspace-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
	}, "agent/task-1", "https://github.com/acme/app.git")
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("error = %v, want unregistered runtime error", err)
	}
}

func TestPublishWorkspaceRevisionCarriesReviewedCommitToRuntime(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	provider := &publishRuntimeProvider{}
	factory := NewFactory(nil, nil).
		WithGitHubToken("ghp_runtime_secret").
		WithRuntimeProvider("docker", provider)

	sessionID := "session-reviewed"
	workspace := &models.Workspace{
		ID:               "workspace-reviewed",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
	}
	revision := vcs.Revision{CommitID: "0123456789abcdef0123456789abcdef01234567", ChangeID: "change-42"}
	if err := factory.publishWorkspaceRevision(context.Background(), workspace, "agent/task-42", revision, "https://github.com/acme/app.git"); err != nil {
		t.Fatalf("publishWorkspaceRevision() error = %v", err)
	}
	if provider.publishReq.SourceRevision != revision {
		t.Fatalf("source revision = %+v, want %+v", provider.publishReq.SourceRevision, revision)
	}
}
