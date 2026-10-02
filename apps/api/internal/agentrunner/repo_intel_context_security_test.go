package agentrunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/projectbrain"
)

func TestRepoIntelContextSourceRejectsChangedPathSymlink(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	secretPath := filepath.Join(outside, "secret.go")
	if err := os.WriteFile(secretPath, []byte("package secret\nfunc hostSecretToken() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	leakPath := filepath.Join(repo, "leak.go")
	if err := os.Symlink(secretPath, leakPath); err != nil {
		t.Fatal(err)
	}

	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Project Brain Test", "GIT_AUTHOR_EMAIL=project-brain@example.invalid",
			"GIT_COMMITTER_NAME=Project Brain Test", "GIT_COMMITTER_EMAIL=project-brain@example.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init")
	runGit("add", "leak.go")
	runGit("commit", "-m", "track symlink")
	head := runGit("rev-parse", "HEAD")

	_, err := NewRepoIntelContextSource(repo).Facts(context.Background(), projectbrain.Query{
		Repository:   "repo-1",
		Revision:     "git-commit:" + head,
		ChangedPaths: []string{"leak.go"},
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Facts() error = %v, want symlink rejection", err)
	}
}

func TestRepoIntelContextSourceRejectsChangedPathTraversal(t *testing.T) {
	repo, head := initRepoIntelTestRepo(t)
	_, err := NewRepoIntelContextSource(repo).Facts(context.Background(), projectbrain.Query{
		Repository:   "repo-1",
		Revision:     "git-commit:" + head,
		ChangedPaths: []string{"../outside.go"},
	})
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("Facts() error = %v, want traversal rejection", err)
	}
}
