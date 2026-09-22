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

	result, err := r.provider.ExecuteCommand(ctx, r.sessionID, runtimes.Command{
		Command:     strings.Join(parts, " "),
		Env:         command.Env,
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
