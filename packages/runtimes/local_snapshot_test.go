package runtimes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalProviderSnapshotUsesActualGitCommit(t *testing.T) {
	worktree := t.TempDir()
	if out, err := exec.Command("git", "init", worktree).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	file := filepath.Join(worktree, "document.txt")
	if err := os.WriteFile(file, []byte("version one"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := &LocalProvider{
		baseDir: t.TempDir(),
		sessions: map[string]*localSession{
			"sess-1": {
				id:           "sess-1",
				workspaceID:  "workspace-1",
				worktreePath: worktree,
				status:       "ready",
				createdAt:    time.Now(),
			},
		},
	}

	snapshot, err := provider.Snapshot(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Snapshot() error: %v", err)
	}
	out, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse: %v: %s", err, out)
	}
	head := strings.TrimSpace(string(out))
	if snapshot.ID != head || snapshot.GitCommit != head {
		t.Fatalf("snapshot = %+v, HEAD = %q", snapshot, head)
	}
	if len(snapshot.GitCommit) < 7 {
		t.Fatalf("GitCommit = %q, want real commit SHA", snapshot.GitCommit)
	}

	if err := os.WriteFile(file, []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := provider.Restore(context.Background(), "sess-1", snapshot); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "version one" {
		t.Fatalf("restored content = %q, want version one", data)
	}
}
