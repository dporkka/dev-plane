package runtimes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalProviderWorkspaceLifecycleUsesVCSBackend(t *testing.T) {
	source := t.TempDir()
	runLocalGitTest(t, source, "init", "-b", "main")
	runLocalGitTest(t, source, "config", "user.name", "Dev Plane Test")
	runLocalGitTest(t, source, "config", "user.email", "dev-plane-test@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	runLocalGitTest(t, source, "add", "-A")
	runLocalGitTest(t, source, "commit", "-m", "initial")

	baseDir := t.TempDir()
	provider := &LocalProvider{baseDir: baseDir, sessions: map[string]*localSession{}}
	session, err := provider.CreateWorkspace(context.Background(), CreateRequest{
		RepositoryID: "repo-1",
		CloneURL:     source,
		Branch:       "agent/task-1",
		BaseBranch:   "main",
		WorktreeName: "workspace-task-1",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	if session.WorktreePath == "" {
		t.Fatal("WorktreePath is empty")
	}
	if got := runLocalGitTest(t, session.WorktreePath, "branch", "--show-current"); got != "agent/task-1" {
		t.Fatalf("branch = %q, want agent/task-1", got)
	}
	if _, err := os.Stat(filepath.Join(session.WorktreePath, "README.md")); err != nil {
		t.Fatalf("workspace README: %v", err)
	}

	sessionDir := filepath.Join(baseDir, session.ID)
	if err := provider.DestroyWorkspace(context.Background(), session.ID); err != nil {
		t.Fatalf("DestroyWorkspace() error = %v", err)
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session directory still exists or stat failed: %v", err)
	}
	if _, ok := provider.sessions[session.ID]; ok {
		t.Fatal("destroyed session remains registered")
	}
}

func runLocalGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}
