package vcs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// JujutsuBackend provides agent-friendly change semantics while preserving a
// colocated Git repository for existing build tools and forges.
type JujutsuBackend struct {
	runner CommandRunner
}

func NewJujutsuBackend(runner CommandRunner) *JujutsuBackend {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &JujutsuBackend{runner: runner}
}

func (b *JujutsuBackend) Name() string { return "jj" }

func (b *JujutsuBackend) CloneOrFetch(ctx context.Context, req CloneRequest) error {
	if err := validateCloneRequest(req); err != nil {
		return err
	}
	if exists(filepath.Join(req.Path, ".jj")) {
		_, err := b.runner.Run(ctx, Command{
			Name: "jj", Args: []string{"git", "fetch", "--remote", "origin"}, Dir: req.Path, Env: req.Env,
		})
		return err
	}
	if err := os.MkdirAll(filepath.Dir(req.Path), 0o755); err != nil {
		return fmt.Errorf("create repository parent: %w", err)
	}
	_, err := b.runner.Run(ctx, Command{
		Name: "jj", Args: []string{"git", "clone", "--colocate", req.URL, req.Path}, Env: req.Env,
	})
	return err
}

func (b *JujutsuBackend) CreateWorkspace(ctx context.Context, req WorkspaceRequest) error {
	if err := validateWorkspaceRequest(req); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(req.WorkspacePath), 0o755); err != nil {
		return fmt.Errorf("create workspace parent: %w", err)
	}
	_, err := b.runner.Run(ctx, Command{
		Name: "jj",
		Args: []string{"workspace", "add", "--name", req.Name, "-r", req.Base, req.WorkspacePath},
		Dir:  req.RepositoryPath,
	})
	return err
}

func (b *JujutsuBackend) RemoveWorkspace(ctx context.Context, req WorkspaceRequest) error {
	if err := validateWorkspaceRequest(req); err != nil {
		return err
	}
	if _, err := b.runner.Run(ctx, Command{
		Name: "jj", Args: []string{"workspace", "forget", req.Name}, Dir: req.RepositoryPath,
	}); err != nil {
		return err
	}
	if err := os.RemoveAll(req.WorkspacePath); err != nil {
		return fmt.Errorf("remove workspace directory: %w", err)
	}
	return nil
}

func (b *JujutsuBackend) Status(ctx context.Context, workspacePath string) (string, error) {
	result, err := b.runner.Run(ctx, Command{Name: "jj", Args: []string{"status"}, Dir: workspacePath})
	return result.Stdout, err
}

func (b *JujutsuBackend) Diff(ctx context.Context, workspacePath string) (string, error) {
	result, err := b.runner.Run(ctx, Command{Name: "jj", Args: []string{"diff", "--git"}, Dir: workspacePath})
	return result.Stdout, err
}

func (b *JujutsuBackend) Snapshot(ctx context.Context, workspacePath, message string) (Revision, error) {
	if strings.TrimSpace(message) == "" {
		return Revision{}, fmt.Errorf("snapshot message is required")
	}
	diff, err := b.runner.Run(ctx, Command{Name: "jj", Args: []string{"diff", "--summary"}, Dir: workspacePath})
	if err != nil {
		return Revision{}, err
	}
	if strings.TrimSpace(diff.Stdout) == "" {
		return b.revision(ctx, workspacePath, "@-")
	}
	if _, err := b.runner.Run(ctx, Command{Name: "jj", Args: []string{"commit", "-m", message}, Dir: workspacePath}); err != nil {
		return Revision{}, err
	}
	return b.revision(ctx, workspacePath, "@-")
}

func (b *JujutsuBackend) Publish(ctx context.Context, req PublishRequest) error {
	if strings.TrimSpace(req.Ref) == "" {
		return fmt.Errorf("publish ref is required")
	}
	if _, err := b.runner.Run(ctx, Command{
		Name: "jj", Args: []string{"bookmark", "set", "--allow-backwards", req.Ref, "-r", "@-"}, Dir: req.WorkspacePath, Env: req.Env,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(req.RemoteURL) != "" {
		if err := validatePublishRemoteURL(req.RemoteURL); err != nil {
			return err
		}
		_, err := b.runner.Run(ctx, Command{
			Name: "git",
			Args: []string{
				"-c", "core.hooksPath=/dev/null",
				"-c", "credential.helper=",
				"-c", "core.askPass=",
				"-c", "http.sslVerify=true",
				"push", "--", req.RemoteURL, req.Ref + ":refs/heads/" + req.Ref,
			},
			Dir: req.WorkspacePath,
			Env: req.Env,
		})
		return err
	}
	_, err := b.runner.Run(ctx, Command{
		Name: "jj", Args: []string{"git", "push", "--remote", "origin", "--bookmark", req.Ref}, Dir: req.WorkspacePath, Env: req.Env,
	})
	return err
}

func (b *JujutsuBackend) revision(ctx context.Context, workspacePath, revset string) (Revision, error) {
	result, err := b.runner.Run(ctx, Command{
		Name: "jj",
		Args: []string{"log", "-r", revset, "--no-graph", "-T", `commit_id ++ "|" ++ change_id.normal_hex() ++ "\n"`},
		Dir:  workspacePath,
	})
	if err != nil {
		return Revision{}, err
	}
	parts := strings.SplitN(strings.TrimSpace(result.Stdout), "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Revision{}, fmt.Errorf("unexpected jj revision output %q", output(result))
	}
	return Revision{CommitID: parts[0], ChangeID: parts[1]}, nil
}
