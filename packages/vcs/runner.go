package vcs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Command is a typed process invocation. Secrets belong in Env, never Args.
type Command struct {
	Name string
	Args []string
	Dir  string
	Env  map[string]string
}

// CommandResult contains process output without forcing callers to parse an
// exec.ExitError directly.
type CommandResult struct {
	Stdout string
	Stderr string
}

// CommandRunner makes VCS backends testable and keeps shell execution behind a
// narrow boundary.
type CommandRunner interface {
	Run(ctx context.Context, cmd Command) (CommandResult, error)
}

// ExecRunner executes commands directly without invoking a shell.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = mergeEnv(os.Environ(), command.Env)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return CommandResult{Stdout: stdout.String(), Stderr: stderr.String()},
			fmt.Errorf("%s failed: %w: %s", command.Name, err, strings.TrimSpace(stderr.String()))
	}
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

func mergeEnv(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides)+1)
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	// VCS operations must never block an autonomous worker waiting for a
	// terminal credential prompt. Callers can still supply GIT_ASKPASS or a
	// credential helper through the environment.
	values["GIT_TERMINAL_PROMPT"] = "0"
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
