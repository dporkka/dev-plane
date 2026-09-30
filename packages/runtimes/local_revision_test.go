package runtimes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalProviderCreateWorkspacePinsRequestedRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	source := t.TempDir()
	runGitForRevisionTest(t, source, "init")
	runGitForRevisionTest(t, source, "checkout", "-b", "main")
	runGitForRevisionTest(t, source, "config", "user.email", "dev-plane@example.invalid")
	runGitForRevisionTest(t, source, "config", "user.name", "Dev Plane")

	file := filepath.Join(source, "state.txt")
	if err := os.WriteFile(file, []byte("first\n"), 0o644); err != nil {
		t.Fatalf("write first state: %v", err)
	}
	runGitForRevisionTest(t, source, "add", "state.txt")
	runGitForRevisionTest(t, source, "commit", "-m", "first")
	first := strings.TrimSpace(runGitForRevisionTest(t, source, "rev-parse", "HEAD"))

	if err := os.WriteFile(file, []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second state: %v", err)
	}
	runGitForRevisionTest(t, source, "add", "state.txt")
	runGitForRevisionTest(t, source, "commit", "-m", "second")
	second := strings.TrimSpace(runGitForRevisionTest(t, source, "rev-parse", "HEAD"))
	if first == second {
		t.Fatal("test fixture commits unexpectedly match")
	}

	provider := NewLocalProvider(t.TempDir())
	session, err := provider.CreateWorkspace(context.Background(), CreateRequest{
		RepositoryID: "repo-1",
		CloneURL:     source,
		Branch:       "agent/task-1",
		BaseBranch:   "main",
		Revision:     first,
		WorktreeName: "workspace-task-1",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace() error: %v", err)
	}
	defer func() {
		if err := provider.DestroyWorkspace(context.Background(), session.ID); err != nil {
			t.Errorf("DestroyWorkspace() error: %v", err)
		}
	}()

	got := strings.TrimSpace(runGitForRevisionTest(t, session.WorktreePath, "rev-parse", "HEAD"))
	if got != first {
		t.Fatalf("workspace HEAD = %q, want pinned revision %q", got, first)
	}
	state, err := os.ReadFile(filepath.Join(session.WorktreePath, "state.txt"))
	if err != nil {
		t.Fatalf("read workspace state: %v", err)
	}
	if string(state) != "first\n" {
		t.Fatalf("workspace state = %q, want first revision contents", string(state))
	}
}

func runGitForRevisionTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}
