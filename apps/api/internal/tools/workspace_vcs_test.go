package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"log/slog"
)

func TestWorkspaceToolsCreateCommitUsesSharedVCSBackend(t *testing.T) {
	dir := t.TempDir()
	runGitForVCSConsolidationTest(t, dir, "init", "-b", "main")
	runGitForVCSConsolidationTest(t, dir, "config", "user.name", "Dev Plane Test")
	runGitForVCSConsolidationTest(t, dir, "config", "user.email", "dev-plane-test@example.invalid")

	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	runGitForVCSConsolidationTest(t, dir, "add", "-A")
	runGitForVCSConsolidationTest(t, dir, "commit", "-m", "initial")

	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	tools := NewWorkspaceTools(slog.Default())
	raw, err := tools.CreateCommit(context.Background(), dir, json.RawMessage(`{"message":"feat: update readme"}`))
	if err != nil {
		t.Fatalf("CreateCommit() error = %v", err)
	}

	var result struct {
		Success    bool   `json:"success"`
		CommitHash string `json:"commit_hash"`
		ChangeID   string `json:"change_id"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Success || result.Error != "" {
		t.Fatalf("result = %+v", result)
	}
	if result.CommitHash == "" || result.ChangeID != result.CommitHash {
		t.Fatalf("revision = (%q, %q)", result.CommitHash, result.ChangeID)
	}

	head := runGitForVCSConsolidationTest(t, dir, "rev-parse", "HEAD")
	if result.CommitHash != head {
		t.Fatalf("commit_hash = %q, HEAD = %q", result.CommitHash, head)
	}
}

func runGitForVCSConsolidationTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
	return string(bytesTrimSpaceForVCSConsolidationTest(out))
}

func bytesTrimSpaceForVCSConsolidationTest(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}
