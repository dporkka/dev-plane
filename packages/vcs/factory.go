package vcs

import (
	"fmt"
	"strings"
)

// NewBackend selects a source-control implementation from configuration while
// keeping callers independent from concrete Git/Jujutsu types.
func NewBackend(name string, runner CommandRunner) (Backend, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "git":
		return NewGitBackend(runner), nil
	case "jj", "jujutsu":
		return NewJujutsuBackend(runner), nil
	default:
		return nil, fmt.Errorf("unsupported VCS backend %q", name)
	}
}
