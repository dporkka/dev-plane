package vcs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GitBackend is the compatibility backend. It uses regular Git worktrees and
// is suitable for repositories/forges that have not enabled Jujutsu yet.
type GitBackend struct {
	runner CommandRunner
}

func NewGitBackend(runner CommandRunner) *GitBackend {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &GitBackend{runner: runner}
}

func (b *GitBackend) Name() string { return "git" }

func (b *GitBackend) CloneOrFetch(ctx context.Context, req CloneRequest) error {
	if err := validateCloneRequest(req); err != nil {
		return err
	}
	if exists(filepath.Join(req.Path, ".git")) {
		_, err := b.runner.Run(ctx, Command{
			Name: "git", Args: []string{"fetch", "origin", "--prune"}, Dir: req.Path, Env: req.Env,
		})
		return err
	}
	if err := os.MkdirAll(filepath.Dir(req.Path), 0o755); err != nil {
		return fmt.Errorf("create repository parent: %w", err)
	}
	_, err := b.runner.Run(ctx, Command{
		Name: "git", Args: []string{"clone", "--", req.URL, req.Path}, Env: req.Env,
	})
	return err
}

func (b *GitBackend) CreateWorkspace(ctx context.Context, req WorkspaceRequest) error {
	if err := validateWorkspaceRequest(req); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(req.WorkspacePath), 0o755); err != nil {
		return fmt.Errorf("create workspace parent: %w", err)
	}
	_, err := b.runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"worktree", "add", "-B", req.Name, req.WorkspacePath, req.Base},
		Dir:  req.RepositoryPath,
	})
	return err
}

func (b *GitBackend) RemoveWorkspace(ctx context.Context, req WorkspaceRequest) error {
	if err := validateWorkspaceRequest(req); err != nil {
		return err
	}
	_, err := b.runner.Run(ctx, Command{
		Name: "git", Args: []string{"worktree", "remove", "--force", req.WorkspacePath}, Dir: req.RepositoryPath,
	})
	return err
}

func (b *GitBackend) Status(ctx context.Context, workspacePath string) (string, error) {
	result, err := b.runner.Run(ctx, Command{Name: "git", Args: []string{"status", "--short"}, Dir: workspacePath})
	return result.Stdout, err
}

func (b *GitBackend) Diff(ctx context.Context, workspacePath string) (string, error) {
	result, err := b.runner.Run(ctx, Command{Name: "git", Args: []string{"diff", "--no-ext-diff", "--binary", "HEAD", "--"}, Dir: workspacePath})
	return result.Stdout, err
}

func (b *GitBackend) Snapshot(ctx context.Context, workspacePath, message string) (Revision, error) {
	if strings.TrimSpace(message) == "" {
		return Revision{}, fmt.Errorf("snapshot message is required")
	}
	if _, err := b.runner.Run(ctx, Command{Name: "git", Args: []string{"add", "-A"}, Dir: workspacePath}); err != nil {
		return Revision{}, err
	}
	changed, err := b.runner.Run(ctx, Command{Name: "git", Args: []string{"diff", "--cached", "--name-only"}, Dir: workspacePath})
	if err != nil {
		return Revision{}, err
	}
	if strings.TrimSpace(changed.Stdout) != "" {
		if _, err := b.runner.Run(ctx, Command{Name: "git", Args: []string{"-c", "user.email=dev-plane@example.invalid", "-c", "user.name=Dev Plane", "commit", "-m", message}, Dir: workspacePath}); err != nil {
			return Revision{}, err
		}
	}
	return b.revision(ctx, workspacePath)
}

func (b *GitBackend) Restore(ctx context.Context, workspacePath, revision string) error {
	if strings.TrimSpace(revision) == "" {
		return fmt.Errorf("restore revision is required")
	}
	_, err := b.runner.Run(ctx, Command{Name: "git", Args: []string{"reset", "--hard", revision}, Dir: workspacePath})
	return err
}

func (b *GitBackend) Publish(ctx context.Context, req PublishRequest) error {
	if strings.TrimSpace(req.Ref) == "" {
		return fmt.Errorf("publish ref is required")
	}
	_, err := b.runner.Run(ctx, Command{Name: "git", Args: []string{"push", "-u", "origin", req.Ref}, Dir: req.WorkspacePath, Env: req.Env})
	return err
}

func (b *GitBackend) revision(ctx context.Context, workspacePath string) (Revision, error) {
	result, err := b.runner.Run(ctx, Command{Name: "git", Args: []string{"rev-parse", "HEAD"}, Dir: workspacePath})
	if err != nil {
		return Revision{}, err
	}
	id := strings.TrimSpace(result.Stdout)
	return Revision{CommitID: id, ChangeID: id}, nil
}
