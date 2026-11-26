package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const configName = ".devx.json"

type config struct {
	Version  int      `json:"version"`
	Delegate []string `json:"delegate"`
}

type invocation struct {
	Dir     string
	Command string
	Args    []string
}

type exitStatusError struct {
	Command string
	Code    int
}

func (e *exitStatusError) Error() string {
	return fmt.Sprintf("%s exited with status %d", e.Command, e.Code)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var statusErr *exitStatusError
	if errors.As(err, &statusErr) {
		return statusErr.Code
	}
	return 1
}

func findRepoRoot(start string) (string, bool) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}

	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, true
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

func resolveInvocation(cwd string, inRepo bool, args []string) (invocation, error) {
	root := cwd
	if inRepo {
		root = cwd
		data, err := os.ReadFile(filepath.Join(root, configName))
		if err == nil {
			var cfg config
			if err := json.Unmarshal(data, &cfg); err != nil {
				return invocation{}, fmt.Errorf("parse %s: %w", configName, err)
			}
			if cfg.Version != 1 {
				return invocation{}, fmt.Errorf("unsupported %s version %d", configName, cfg.Version)
			}
			if len(cfg.Delegate) == 0 || cfg.Delegate[0] == "" {
				return invocation{}, fmt.Errorf("%s delegate must contain at least one command", configName)
			}
			delegateArgs := append([]string{}, cfg.Delegate[1:]...)
			delegateArgs = append(delegateArgs, args...)
			return invocation{Dir: root, Command: cfg.Delegate[0], Args: delegateArgs}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return invocation{}, fmt.Errorf("read %s: %w", configName, err)
		}
	}

	return invocation{Dir: root, Command: "dev-plane", Args: append([]string{}, args...)}, nil
}

func run(args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, ok := findRepoRoot(cwd)
	if ok {
		cwd = root
	}

	inv, err := resolveInvocation(cwd, ok, args)
	if err != nil {
		return err
	}

	cmd := exec.Command(inv.Command, inv.Args...)
	cmd.Dir = inv.Dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &exitStatusError{Command: inv.Command, Code: exitErr.ExitCode()}
		}
		return err
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "devx: %v\n", err)
		os.Exit(exitCode(err))
	}
}
