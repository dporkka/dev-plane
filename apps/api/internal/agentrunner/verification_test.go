package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/models"
)

func TestRunFinalChecksUsesConfiguredPlanAndBindsEvidenceToWorkspaceRevision(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.invalid")
	runGit(t, repo, "config", "user.name", "Dev Plane")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "initial")

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE project_configs (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			test_command TEXT,
			lint_command TEXT,
			typecheck_command TEXT,
			build_command TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO project_configs (
			id, repository_id, test_command, lint_command, typecheck_command, build_command
		) VALUES (
			'config-1', 'repo-1',
			'test -f README.md',
			'test -s README.md',
			NULL,
			'printf build-ok >/dev/null'
		);
	`)
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default())
	task := &models.Task{RepositoryID: "repo-1"}
	workspace := &models.Workspace{ID: "workspace-1", RuntimeProvider: "local"}

	results := runner.runFinalChecks(context.Background(), &models.AgentRun{ID: "run-1"}, task, workspace, repo)

	revision, _ := results["subject_revision"].(string)
	if !strings.HasPrefix(revision, "tree:") || len(strings.TrimPrefix(revision, "tree:")) < 8 {
		t.Fatalf("subject_revision = %q, want tree digest", revision)
	}

	raw, err := json.Marshal(results["evidence"])
	if err != nil {
		t.Fatal(err)
	}
	var evidence []struct {
		Name            string `json:"name"`
		Status          string `json:"status"`
		Command         string `json:"command"`
		SubjectRevision string `json:"subject_revision"`
	}
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 3 {
		t.Fatalf("evidence = %#v, want tests/lint/build", evidence)
	}
	wantNames := []string{"tests", "lint", "build"}
	for i, want := range wantNames {
		if evidence[i].Name != want || evidence[i].Status != "passed" {
			t.Fatalf("evidence[%d] = %#v", i, evidence[i])
		}
		if evidence[i].SubjectRevision != revision {
			t.Fatalf("evidence[%d] revision = %q, want %q", i, evidence[i].SubjectRevision, revision)
		}
	}
}

func TestWorkspaceSubjectRevisionIncludesDirtyTrackedAndUntrackedContentWithoutChangingIndex(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.invalid")
	runGit(t, repo, "config", "user.name", "Dev Plane")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "initial")

	beforeIndex := runGit(t, repo, "write-tree")
	before, err := localWorkspaceSubjectRevision(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := localWorkspaceSubjectRevision(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatalf("subject revision did not change: %q", before)
	}
	if got := runGit(t, repo, "write-tree"); got != beforeIndex {
		t.Fatalf("real index changed: before=%q after=%q", beforeIndex, got)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRunFinalChecksReportsConfiguredFailure(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.invalid")
	runGit(t, repo, "config", "user.name", "Dev Plane")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "initial")

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE project_configs (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			test_command TEXT,
			lint_command TEXT,
			typecheck_command TEXT,
			build_command TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO project_configs (id, repository_id, test_command)
		VALUES ('config-1', 'repo-1', 'false');
	`)
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default())
	results := runner.runFinalChecks(
		context.Background(),
		&models.AgentRun{ID: "run-1"},
		&models.Task{RepositoryID: "repo-1"},
		&models.Workspace{ID: "workspace-1", RuntimeProvider: "local"},
		repo,
	)

	if passed, _ := results["passed"].(bool); passed {
		t.Fatalf("passed = true, want false: %#v", results)
	}
	raw, _ := json.Marshal(results["evidence"])
	if !strings.Contains(string(raw), `"status":"failed"`) {
		t.Fatalf("evidence = %s, want failed status", raw)
	}
}

func TestRunFinalChecksFailsEvidenceWhenCheckMutatesWorkspace(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.invalid")
	runGit(t, repo, "config", "user.name", "Dev Plane")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "initial")

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE project_configs (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			test_command TEXT,
			lint_command TEXT,
			typecheck_command TEXT,
			build_command TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO project_configs (id, repository_id, test_command)
		VALUES ('config-1', 'repo-1', 'printf changed > generated.txt');
	`)
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default())
	results := runner.runFinalChecks(
		context.Background(),
		&models.AgentRun{ID: "run-1"},
		&models.Task{RepositoryID: "repo-1"},
		&models.Workspace{ID: "workspace-1", RuntimeProvider: "local"},
		repo,
	)

	if changed, _ := results["workspace_changed_during_verification"].(bool); !changed {
		t.Fatalf("workspace_changed_during_verification = false: %#v", results)
	}
	if passed, _ := results["passed"].(bool); passed {
		t.Fatalf("passed = true, want false: %#v", results)
	}
	raw, _ := json.Marshal(results["evidence"])
	if !strings.Contains(string(raw), `"status":"failed"`) {
		t.Fatalf("evidence = %s, want failed status", raw)
	}
}
