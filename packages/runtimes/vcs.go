package runtimes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/vcs"
)


// VCSPublishRequest is the runtime-facing alias of the canonical VCS publish
// contract.
type VCSPublishRequest = vcs.PublishRequest

// VCSWorkspacePublisher is the privileged publication capability for runtime
// workspaces. It is deliberately separate from Provider.ExecuteCommand: agent
// sandboxes may have no network, while the runtime control plane can publish a
// reviewed revision using trusted repository metadata and credentials.
type VCSWorkspacePublisher interface {
	PublishVCS(ctx context.Context, sessionID string, req vcs.PublishRequest) error
}

func (p *LocalProvider) PublishVCS(ctx context.Context, sessionID string, req vcs.PublishRequest) error {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	req.WorkspacePath = sess.worktreePath
	return vcs.NewGitBackend(nil).Publish(ctx, req)
}

func (p *DockerProvider) PublishVCS(ctx context.Context, sessionID string, req vcs.PublishRequest) error {
	sess, err := p.session(sessionID)
	if err != nil {
		return err
	}

	stagingRoot, err := os.MkdirTemp(p.baseDir, "publish-"+sessionID+"-*")
	if err != nil {
		return fmt.Errorf("create publish staging directory: %w", err)
	}
	defer os.RemoveAll(stagingRoot)

	stagingWorkspace := filepath.Join(stagingRoot, "workspace")
	if err := os.MkdirAll(stagingWorkspace, 0o700); err != nil {
		return fmt.Errorf("create publish workspace: %w", err)
	}
	source := sess.container + ":" + workspaceDir + "/."
	if out, err := p.runner.Run(ctx, "docker", []string{"cp", source, stagingWorkspace}, commandOptions{}); err != nil {
		return fmt.Errorf("copy workspace for publish: %w (stderr: %s)", err, strings.TrimSpace(out.Stderr))
	}

	req.WorkspacePath = stagingWorkspace
	backend := vcs.NewGitBackend(newHostVCSCommandRunner(p.runner))
	return backend.Publish(ctx, req)
}

// newHostVCSCommandRunner adapts the runtime package's host command runner to
// the shared VCS interface. Docker uses this for staging repository operations
// before the workspace is copied into the isolated volume.
func newHostVCSCommandRunner(runner commandRunner) vcs.CommandRunner {
	return hostVCSRunner{runner: runner}
}

type hostVCSRunner struct {
	runner commandRunner
}

func (r hostVCSRunner) Run(ctx context.Context, command vcs.Command) (vcs.CommandResult, error) {
	if r.runner == nil {
		return vcs.CommandResult{}, fmt.Errorf("host command runner is required")
	}
	out, err := r.runner.Run(ctx, command.Name, command.Args, commandOptions{
		Dir: command.Dir,
		Env: command.Env,
	})
	result := vcs.CommandResult{Stdout: out.Stdout, Stderr: out.Stderr}
	if err != nil {
		return result, err
	}
	if out.ExitCode != 0 {
		return result, fmt.Errorf("%s failed with exit code %d: %s", command.Name, out.ExitCode, strings.TrimSpace(out.Stderr))
	}
	return result, nil
}

// NewVCSCommandRunner adapts the runtime Provider boundary to the shared VCS
// command interface. Source-control semantics stay in packages/vcs while the
// runtime remains responsible for process isolation and transport.
func NewVCSCommandRunner(provider Provider, sessionID string) vcs.CommandRunner {
	return runtimeVCSRunner{provider: provider, sessionID: sessionID}
}

type runtimeVCSRunner struct {
	provider  Provider
	sessionID string
}

func (r runtimeVCSRunner) Run(ctx context.Context, command vcs.Command) (vcs.CommandResult, error) {
	if r.provider == nil {
		return vcs.CommandResult{}, fmt.Errorf("runtime provider is required")
	}
	if strings.TrimSpace(r.sessionID) == "" {
		return vcs.CommandResult{}, fmt.Errorf("runtime session ID is required")
	}
	if strings.TrimSpace(command.Name) == "" {
		return vcs.CommandResult{}, fmt.Errorf("VCS command name is required")
	}

	parts := make([]string, 0, len(command.Args)+1)
	parts = append(parts, vcsShellQuote(command.Name))
	for _, arg := range command.Args {
		parts = append(parts, vcsShellQuote(arg))
	}

	env := make(map[string]string, len(command.Env)+5)
	for key, value := range command.Env {
		env[key] = value
	}
	if _, ok := env["GIT_TERMINAL_PROMPT"]; !ok {
		env["GIT_TERMINAL_PROMPT"] = "0"
	}
	if command.Name == "git" {
		if _, ok := env["GIT_AUTHOR_NAME"]; !ok {
			env["GIT_AUTHOR_NAME"] = "Dev Plane"
		}
		if _, ok := env["GIT_AUTHOR_EMAIL"]; !ok {
			env["GIT_AUTHOR_EMAIL"] = "dev-plane@example.invalid"
		}
		if _, ok := env["GIT_COMMITTER_NAME"]; !ok {
			env["GIT_COMMITTER_NAME"] = "Dev Plane"
		}
		if _, ok := env["GIT_COMMITTER_EMAIL"]; !ok {
			env["GIT_COMMITTER_EMAIL"] = "dev-plane@example.invalid"
		}
	}

	result, err := r.provider.ExecuteCommand(ctx, r.sessionID, Command{
		Command:     strings.Join(parts, " "),
		Dir:         command.Dir,
		Env:         env,
		Timeout:     60 * time.Second,
		UnsafeShell: true,
	})
	if result == nil {
		if err != nil {
			return vcs.CommandResult{}, err
		}
		return vcs.CommandResult{}, fmt.Errorf("runtime provider returned no command result")
	}

	out := vcs.CommandResult{Stdout: result.Stdout, Stderr: result.Stderr}
	if err != nil {
		return out, err
	}
	if result.ExitCode != 0 {
		return out, fmt.Errorf("%s failed with exit code %d: %s", command.Name, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return out, nil
}

func vcsShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
