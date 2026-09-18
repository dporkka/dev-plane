package vcs

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

func validateCloneRequest(req CloneRequest) error {
	if strings.TrimSpace(req.URL) == "" {
		return fmt.Errorf("repository URL is required")
	}
	if strings.TrimSpace(req.Path) == "" {
		return fmt.Errorf("repository path is required")
	}
	parsed, err := url.Parse(req.URL)
	if err != nil {
		return fmt.Errorf("parse repository URL: %w", err)
	}
	if parsed.User != nil {
		return fmt.Errorf("repository URL must not contain credentials; use environment-based authentication")
	}
	return nil
}

func validateWorkspaceRequest(req WorkspaceRequest) error {
	if strings.TrimSpace(req.RepositoryPath) == "" {
		return fmt.Errorf("repository path is required")
	}
	if strings.TrimSpace(req.WorkspacePath) == "" {
		return fmt.Errorf("workspace path is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("workspace name is required")
	}
	if strings.TrimSpace(req.Base) == "" {
		return fmt.Errorf("workspace base revision is required")
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func output(result CommandResult) string {
	if strings.TrimSpace(result.Stdout) != "" {
		return strings.TrimSpace(result.Stdout)
	}
	return strings.TrimSpace(result.Stderr)
}
