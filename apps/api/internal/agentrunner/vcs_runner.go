package agentrunner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/vcs"
)

// runtimeVCSRunner adapts typed VCS commands to an isolated runtime session.
// The VCS package owns source-control semantics; this adapter only transports
// those commands across the runtime boundary.
type runtimeVCSRunner struct {
	provider  runtimes.Provider
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
	parts = append(parts, shellQuote(command.Name))
	for _, arg := range command.Args {
		parts = append(parts, shellQuote(arg))
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

	result, err := r.provider.ExecuteCommand(ctx, r.sessionID, runtimes.Command{
		Command:     strings.Join(parts, " "),
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
