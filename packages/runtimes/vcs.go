package runtimes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/vcs"
)

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
