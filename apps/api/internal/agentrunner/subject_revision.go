package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/runtimes"
)

// workspaceSubjectRevision returns the same canonical git-tree:<tree-sha>
// identity used by the task-capsule final-verification authority.
//
// A temporary Git index includes tracked modifications, deletions, and untracked
// files without changing the workspace's real index.
func (r *Runner) workspaceSubjectRevision(
	ctx context.Context,
	workspace *models.Workspace,
	workspacePath string,
) (string, error) {
	provider, sessionID, err := r.runtimeProviderForWorkspace(ctx, workspace)
	if err != nil {
		return "", err
	}
	if provider != nil {
		return runtimeSubjectRevision(ctx, provider, sessionID)
	}
	return localSubjectRevision(ctx, workspacePath)
}

func runtimeSubjectRevision(ctx context.Context, provider runtimes.Provider, sessionID string) (string, error) {
	const command = `tmp=$(mktemp); rm -f "$tmp"; trap 'rm -f "$tmp"' EXIT; GIT_INDEX_FILE="$tmp" git read-tree HEAD >/dev/null && GIT_INDEX_FILE="$tmp" git add -A >/dev/null && GIT_INDEX_FILE="$tmp" git write-tree`
	result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Command:     command,
		Timeout:     30 * time.Second,
		UnsafeShell: true,
	})
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", errors.New("workspace revision command returned no result")
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("workspace revision command failed: %s", strings.TrimSpace(result.Stdout+result.Stderr))
	}
	revision := strings.TrimSpace(result.Stdout)
	if revision == "" {
		return "", errors.New("workspace revision is empty")
	}
	return "git-tree:" + revision, nil
}

func localSubjectRevision(ctx context.Context, workspacePath string) (string, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "", errors.New("workspace path is required")
	}

	indexFile, err := os.CreateTemp("", "dev-plane-verification-index-*")
	if err != nil {
		return "", fmt.Errorf("create temporary git index: %w", err)
	}
	indexPath := indexFile.Name()
	if err := indexFile.Close(); err != nil {
		_ = os.Remove(indexPath)
		return "", fmt.Errorf("close temporary git index: %w", err)
	}
	if err := os.Remove(indexPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("prepare temporary git index: %w", err)
	}
	defer os.Remove(indexPath)

	runGit := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workspacePath
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
		return output, nil
	}
	if _, err := runGit("read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := runGit("add", "-A"); err != nil {
		return "", err
	}
	output, err := runGit("write-tree")
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(string(output))
	if revision == "" {
		return "", errors.New("workspace revision is empty")
	}
	return "git-tree:" + revision, nil
}
