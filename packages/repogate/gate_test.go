package repogate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestPrepareAndIntegrateApprovedCandidate(t *testing.T) {
	repo := testRepository(t)
	db := testDatabase(t)
	seedGateContext(t, db, repo, true, "")
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("base\nagent change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := New(db, nil)
	prepared, err := gate.PrepareReviewedCandidate(context.Background(), "run-1", "task-1")
	if err != nil {
		t.Fatalf("PrepareReviewedCandidate() error = %v", err)
	}
	if prepared.Status != StatusVerified || prepared.CommitID == "" {
		t.Fatalf("prepared = %+v", prepared)
	}
	integrated, err := gate.IntegrateApproved(context.Background(), "run-1", "task-1")
	if err != nil {
		t.Fatalf("IntegrateApproved() error = %v", err)
	}
	if integrated.Status != StatusIntegrated {
		t.Fatalf("status = %q", integrated.Status)
	}
	if integrated.IntegrationBranch != "dev-plane/integrated/task-1" {
		t.Fatalf("branch = %q", integrated.IntegrationBranch)
	}
	if got := gitOutput(t, repo, "show", integrated.IntegrationBranch+":app.txt"); got != "base\nagent change" {
		t.Fatalf("integrated file = %q", got)
	}
	var workspaceBranch string
	if err := db.QueryRow("SELECT branch FROM workspaces WHERE id = 'workspace-1'").Scan(&workspaceBranch); err != nil {
		t.Fatal(err)
	}
	if workspaceBranch != integrated.IntegrationBranch {
		t.Fatalf("workspace branch = %q", workspaceBranch)
	}
	again, err := gate.IntegrateApproved(context.Background(), "run-1", "task-1")
	if err != nil {
		t.Fatalf("second IntegrateApproved() error = %v", err)
	}
	if again.IntegratedCommit != integrated.IntegratedCommit {
		t.Fatalf("idempotent commit = %q, want %q", again.IntegratedCommit, integrated.IntegratedCommit)
	}
}

func TestPrepareReviewedCandidateBlocksNonApprovableReview(t *testing.T) {
	repo := testRepository(t)
	db := testDatabase(t)
	seedGateContext(t, db, repo, false, "")
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(db, nil).PrepareReviewedCandidate(context.Background(), "run-1", "task-1")
	if !errors.Is(err, ErrReviewNotApprovable) {
		t.Fatalf("error = %v, want ErrReviewNotApprovable", err)
	}
}

func TestPrepareReviewedCandidatePersistsVerificationRejection(t *testing.T) {
	repo := testRepository(t)
	db := testDatabase(t)
	seedGateContext(t, db, repo, true, "git diff --exit-code HEAD^ HEAD --")
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, err := New(db, nil).PrepareReviewedCandidate(context.Background(), "run-1", "task-1")
	if !errors.Is(err, ErrCandidateRejected) {
		t.Fatalf("error = %v, want ErrCandidateRejected", err)
	}
	if record.Status != StatusRejected {
		t.Fatalf("status = %q", record.Status)
	}
}

func testRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "app.txt")
	runGit(t, repo, "commit", "-m", "base")
	runGit(t, repo, "checkout", "-b", "agent/task-1")
	return repo
}

func testDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			target_branch TEXT,
			deleted_at DATETIME
		);
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			branch TEXT NOT NULL,
			base_branch TEXT NOT NULL,
			worktree_path TEXT,
			runtime_provider TEXT,
			runtime_session_id TEXT,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT,
			agent_role TEXT NOT NULL
		);
		CREATE TABLE review_reports (
			run_id TEXT PRIMARY KEY,
			approvable BOOLEAN NOT NULL
		);
		CREATE TABLE project_configs (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			test_command TEXT,
			lint_command TEXT,
			typecheck_command TEXT,
			build_command TEXT,
			updated_at DATETIME
		);
		CREATE TABLE verified_candidates (
			run_id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			commit_id TEXT NOT NULL,
			change_id TEXT,
			source_branch TEXT NOT NULL,
			target_branch TEXT NOT NULL,
			integration_branch TEXT,
			integrated_commit TEXT,
			status TEXT NOT NULL,
			initial_verification TEXT NOT NULL DEFAULT '{}',
			final_verification TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME,
			updated_at DATETIME
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func seedGateContext(t *testing.T, db *sql.DB, repo string, approvable bool, testCommand string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO tasks (id, repository_id, target_branch) VALUES ('task-1', 'repo-1', 'main');
		INSERT INTO workspaces (
			id, branch, base_branch, worktree_path, runtime_provider, runtime_session_id
		) VALUES ('workspace-1', 'agent/task-1', 'main', ?, 'local', NULL);
		INSERT INTO agent_runs (id, task_id, workspace_id, agent_role)
		VALUES ('run-1', 'task-1', 'workspace-1', 'implementer');
		INSERT INTO review_reports (run_id, approvable) VALUES ('run-1', ?);
		INSERT INTO project_configs (
			id, repository_id, test_command, lint_command, typecheck_command, build_command, updated_at
		) VALUES ('config-1', 'repo-1', ?, '', '', '', CURRENT_TIMESTAMP);
	`, repo, approvable, testCommand)
	if err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
