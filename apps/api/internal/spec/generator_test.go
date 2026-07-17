package spec

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGenerate_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	g := NewGenerator(db, slog.Default())

	taskID := "task-1"
	repoID := "repo-1"
	now := time.Now()

	// Expect loadTask query (24 columns including deleted_at)
	mock.ExpectQuery("SELECT id, project_id, repository_id").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "repository_id", "workspace_id", "created_by",
			"source", "source_id", "title", "description", "status", "priority",
			"risk_level", "target_branch", "spec", "acceptance_criteria",
			"max_cost", "max_runtime_minutes", "approval_requirements", "metadata",
			"started_at", "completed_at", "created_at", "updated_at", "deleted_at",
		}).AddRow(
			taskID, "proj-1", repoID, nil, "user-1", "web", nil,
			"Fix login bug", "The login page crashes", "backlog", "high",
			"medium", "main", nil, nil,
			nil, 60, nil, nil,
			nil, nil, now, now, nil,
		))

	// Expect loadRepository query
	mock.ExpectQuery("SELECT id, project_id, github_id").
		WithArgs(repoID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "github_id", "owner", "name", "full_name", "clone_url",
			"default_branch", "private", "connection_status", "last_synced_at",
			"webhook_secret", "settings", "created_at", "updated_at",
		}).AddRow(
			repoID, "proj-1", nil, "owner", "repo", "owner/repo", "https://github.com/owner/repo.git",
			"main", false, "connected", now, nil, nil, now, now,
		))

	// Expect loadProjectConfig query
	mock.ExpectQuery("SELECT id, repository_id, package_manager").
		WithArgs(repoID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "repository_id", "package_manager", "framework", "test_command",
			"lint_command", "typecheck_command", "dev_command", "build_command",
			"has_dockerfile", "has_devcontainer", "detected_at", "updated_at",
		}).AddRow(
			"cfg-1", repoID, "npm", "nextjs", "npm test",
			"npm run lint", "npm run typecheck", "npm run dev", "npm run build",
			true, false, now, now,
		))

	// Expect saveSpec: INSERT INTO task_specs
	mock.ExpectExec("INSERT INTO task_specs").
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Expect updateTaskStatus: UPDATE tasks SET status=$2, updated_at=$3 WHERE id=$1
	mock.ExpectExec("UPDATE tasks SET status").
		WithArgs(taskID, "spec_review", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	spec, err := g.Generate(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if spec.TaskID != taskID {
		t.Errorf("expected task ID %q, got %q", taskID, spec.TaskID)
	}
	if spec.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(spec.ImplementationPlan) == 0 {
		t.Error("expected at least one implementation step")
	}
}

func TestGenerate_TaskNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	g := NewGenerator(db, slog.Default())

	taskID := "nonexistent"

	mock.ExpectQuery("SELECT id, project_id, repository_id").
		WithArgs(taskID).
		WillReturnError(sql.ErrNoRows)

	_, err = g.Generate(context.Background(), taskID)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestGetSpec_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	g := NewGenerator(db, slog.Default())

	taskID := "task-1"
	specID := "spec-1"
	now := time.Now()

	mock.ExpectQuery("SELECT id, task_id, summary, problem_statement").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "summary", "problem_statement", "implementation_plan",
			"files_to_change", "files_to_create", "acceptance_criteria", "test_plan",
			"risk_assessment", "rollback_plan", "required_approvals", "estimated_cost",
			"recommended_agent", "generated_by", "generated_at",
		}).AddRow(
			specID, taskID, "Fix login", "Login is broken", "[\"step 1\"]",
			"[\"auth.go\"]", "[\"auth_test.go\"]", "[\"login works\"]", "unit tests",
			"low risk", "revert", "[]", 0.0,
			"implementer", "heuristic", now,
		))

	spec, err := g.GetSpec(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetSpec() error: %v", err)
	}
	if spec.TaskID != taskID {
		t.Errorf("expected task ID %q, got %q", taskID, spec.TaskID)
	}
}
