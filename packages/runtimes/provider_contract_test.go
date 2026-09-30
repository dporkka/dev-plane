package runtimes_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/runtimes/contracttest"
)

func TestLocalProviderContract(t *testing.T) {
	t.Setenv("WORKSPACE_TMPFS_DISABLE", "1")

	contracttest.Run(t,
		func(t *testing.T) runtimes.Provider {
			return runtimes.NewLocalProvider(t.TempDir())
		},
		func(t *testing.T) runtimes.CreateRequest {
			source := t.TempDir()
			runGit(t, source, "init", "-b", "main")
			runGit(t, source, "config", "user.email", "contract@example.invalid")
			runGit(t, source, "config", "user.name", "Runtime Contract")
			if err := os.WriteFile(filepath.Join(source, "seed.txt"), []byte("before\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, source, "add", "seed.txt")
			runGit(t, source, "commit", "-m", "initial")

			return runtimes.CreateRequest{
				RepositoryID: "contract-fixture",
				CloneURL:     source,
				Branch:       "contract-work",
				BaseBranch:   "main",
				WorktreeName: "worktree",
			}
		},
	)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
