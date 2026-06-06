package repointel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeManager handles git clone, worktree creation, and branch management
// for isolated development environments.
type WorktreeManager struct {
	baseDir string // Base directory for all workspace repos
}

// NewWorktreeManager creates a new WorktreeManager with the given base directory.
func NewWorktreeManager(baseDir string) *WorktreeManager {
	return &WorktreeManager{baseDir: baseDir}
}

// BaseDir returns the root directory where repositories are cloned.
func (wm *WorktreeManager) BaseDir() string {
	return wm.baseDir
}

// RepoDir returns the path where a repository is cloned.
func (wm *WorktreeManager) RepoDir(repoName string) string {
	return filepath.Join(wm.baseDir, repoName)
}

// WorktreeDir returns the path for a specific worktree.
func (wm *WorktreeManager) WorktreeDir(repoName, branch string) string {
	safeBranch := strings.ReplaceAll(branch, "/", "-")
	return filepath.Join(wm.baseDir, repoName, "worktrees", safeBranch)
}

// Clone clones a repository using an optional OAuth token for authentication.
// The repository is cloned into the base directory.
func (wm *WorktreeManager) Clone(ctx context.Context, cloneURL, repoDir string, token string) error {
	targetDir := filepath.Join(wm.baseDir, repoDir)

	// Check if already cloned
	if _, err := os.Stat(filepath.Join(targetDir, ".git")); err == nil {
		// Already cloned, do a fetch instead
		cmd := exec.CommandContext(ctx, "git", "-C", targetDir, "fetch", "origin")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git fetch: %w (output: %s)", err, string(out))
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
		return fmt.Errorf("create repo parent directory: %w", err)
	}

	// Build clone command with optional token auth
	var cmd *exec.Cmd
	if token != "" {
		authURL := insertTokenIntoURL(cloneURL, token)
		cmd = exec.CommandContext(ctx, "git", "clone", "--", authURL, targetDir)
	} else {
		cmd = exec.CommandContext(ctx, "git", "clone", "--", cloneURL, targetDir)
	}

	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %w (output: %s)", err, string(out))
	}

	return nil
}

// CreateWorktree creates a new git worktree for a branch.
// The worktree is created from the given base branch.
func (wm *WorktreeManager) CreateWorktree(ctx context.Context, repoDir, branch, worktreePath string) error {
	fullRepoDir := filepath.Join(wm.baseDir, repoDir)

	// Ensure the parent directory exists
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0755); err != nil {
		return fmt.Errorf("create worktree parent directory: %w", err)
	}

	// Create worktree with new branch
	cmd := exec.CommandContext(ctx, "git", "-C", fullRepoDir, "worktree", "add", "-B", branch, worktreePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Worktree may already exist
		if strings.Contains(string(out), "already exists") {
			return nil
		}
		return fmt.Errorf("git worktree add: %w (output: %s)", err, string(out))
	}

	return nil
}

// RemoveWorktree removes a git worktree.
func (wm *WorktreeManager) RemoveWorktree(ctx context.Context, repoDir, worktreePath string) error {
	fullRepoDir := filepath.Join(wm.baseDir, repoDir)

	cmd := exec.CommandContext(ctx, "git", "-C", fullRepoDir, "worktree", "remove", "--force", worktreePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w (output: %s)", err, string(out))
	}

	return nil
}

// GetDiff returns the git diff of uncommitted changes in the worktree.
func (wm *WorktreeManager) GetDiff(ctx context.Context, worktreePath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "diff", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff: %w (output: %s)", err, string(out))
	}
	return string(out), nil
}

// GetDiffStaged returns the git diff of staged changes.
func (wm *WorktreeManager) GetDiffStaged(ctx context.Context, worktreePath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "diff", "--staged")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff staged: %w (output: %s)", err, string(out))
	}
	return string(out), nil
}

// Commit stages all changes and creates a commit with the given message.
func (wm *WorktreeManager) Commit(ctx context.Context, worktreePath, message string) error {
	// Stage all changes
	addCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w (output: %s)", err, string(out))
	}

	// Check if there are changes to commit
	diffCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "diff", "--staged", "--quiet")
	if err := diffCmd.Run(); err == nil {
		// No changes to commit
		return nil
	}

	// Create commit
	commitCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "commit", "-m", message)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w (output: %s)", err, string(out))
	}

	return nil
}

// Push pushes the given branch to origin.
func (wm *WorktreeManager) Push(ctx context.Context, worktreePath, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "push", "-u", "origin", branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %w (output: %s)", err, string(out))
	}
	return nil
}

// GetStatus returns the git status of the worktree as a short-format string.
func (wm *WorktreeManager) GetStatus(ctx context.Context, worktreePath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--short")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git status: %w (output: %s)", err, string(out))
	}
	return string(out), nil
}

// GetBranch returns the current branch name.
func (wm *WorktreeManager) GetBranch(ctx context.Context, worktreePath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git branch: %w (output: %s)", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// GetLastCommit returns the hash and message of the most recent commit.
func (wm *WorktreeManager) GetLastCommit(ctx context.Context, worktreePath string) (hash, message string, err error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "log", "-1", "--format=%H|%s")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("git log: %w (output: %s)", err, string(out))
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return strings.TrimSpace(string(out)), "", nil
}

// Pull fetches and merges changes from the remote branch.
func (wm *WorktreeManager) Pull(ctx context.Context, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "pull")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git pull: %w (output: %s)", err, string(out))
	}
	return nil
}

// Fetch fetches updates from origin without merging.
func (wm *WorktreeManager) Fetch(ctx context.Context, repoDir string) error {
	fullRepoDir := filepath.Join(wm.baseDir, repoDir)
	cmd := exec.CommandContext(ctx, "git", "-C", fullRepoDir, "fetch", "origin")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %w (output: %s)", err, string(out))
	}
	return nil
}

// ListWorktrees returns all worktrees for a repository.
func (wm *WorktreeManager) ListWorktrees(ctx context.Context, repoDir string) ([]string, error) {
	fullRepoDir := filepath.Join(wm.baseDir, repoDir)
	cmd := exec.CommandContext(ctx, "git", "-C", fullRepoDir, "worktree", "list", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w (output: %s)", err, string(out))
	}

	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths, nil
}

// insertTokenIntoURL inserts an OAuth token into a GitHub HTTPS URL.
func insertTokenIntoURL(cloneURL, token string) string {
	if token == "" {
		return cloneURL
	}
	if !strings.HasPrefix(cloneURL, "https://github.com/") {
		return cloneURL
	}
	return strings.Replace(cloneURL, "https://", "https://"+token+"@", 1)
}
