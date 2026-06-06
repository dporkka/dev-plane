package repointel

import (
	"testing"
)

func assertEqual(t *testing.T, got, want interface{}) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func assertTrue(t *testing.T, got bool, msg string) {
	t.Helper()
	if !got {
		t.Errorf("expected true: %s", msg)
	}
}

func TestNewWorktreeManager(t *testing.T) {
	wm := NewWorktreeManager("/tmp/workspaces")
	assertTrue(t, wm != nil, "manager should not be nil")
	assertEqual(t, wm.BaseDir(), "/tmp/workspaces")
}

func TestWorktreeManager_BaseDir(t *testing.T) {
	wm := NewWorktreeManager("/data/repos")
	assertEqual(t, wm.BaseDir(), "/data/repos")
}

func TestWorktreeManager_RepoDir(t *testing.T) {
	wm := NewWorktreeManager("/data/repos")
	assertEqual(t, wm.RepoDir("my-repo"), "/data/repos/my-repo")
}

func TestWorktreeManager_WorktreeDir(t *testing.T) {
	wm := NewWorktreeManager("/data/repos")
	dir := wm.WorktreeDir("my-repo", "feature-auth")
	assertEqual(t, dir, "/data/repos/my-repo/worktrees/feature-auth")
}

func TestWorktreeManager_WorktreeDir_SanitizesBranch(t *testing.T) {
	wm := NewWorktreeManager("/data/repos")
	dir := wm.WorktreeDir("my-repo", "feature/new-auth")
	assertEqual(t, dir, "/data/repos/my-repo/worktrees/feature-new-auth")
}

func TestInsertTokenIntoURL(t *testing.T) {
	url := insertTokenIntoURL("https://github.com/owner/repo.git", "ghp_token123")
	assertEqual(t, url, "https://ghp_token123@github.com/owner/repo.git")
}

func TestInsertTokenIntoURL_NonGitHub(t *testing.T) {
	url := insertTokenIntoURL("https://gitlab.com/owner/repo.git", "token123")
	assertEqual(t, url, "https://gitlab.com/owner/repo.git")
}

func TestInsertTokenIntoURL_EmptyToken(t *testing.T) {
	original := "https://github.com/owner/repo.git"
	url := insertTokenIntoURL(original, "")
	assertEqual(t, url, original)
}
