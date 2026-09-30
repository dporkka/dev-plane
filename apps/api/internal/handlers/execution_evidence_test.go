package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func TestListTaskExecutionEvidence(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	now := time.Now().UTC()
	expectAuthorizeTask(mock, taskID)

	mock.ExpectQuery("SELECT id, status, outcome, error_message, summary").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status", "outcome", "error_message", "summary"}).
			AddRow("run-1", "completed", "passed", nil, "implemented change").
			AddRow("run-2", "failed", "failed", "verification rejected candidate", "review repair needed"))

	mock.ExpectQuery("SELECT s.id, s.agent_run_id, s.tool_name, s.status, s.outcome").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "agent_run_id", "tool_name", "status", "outcome", "output", "tool_output", "exit_code", "created_at",
		}).
			AddRow("step-commit", "run-1", "create_commit", "completed", "passed",
				`{"success":true,"commit_hash":"abc123","output":"committed"}`, nil, nil, now).
			AddRow("step-tests", "run-1", "run_tests", "completed", "passed",
				`{"passed":true,"total":18,"failed":0,"skipped":1,"duration_ms":4200,"exit_code":0,"output":"ok"}`, nil, 0, now))

	mock.ExpectQuery("SELECT rr.run_id, rr.summary, rr.risk_level, rr.approvable").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"run_id", "summary", "risk_level", "approvable", "findings", "suggestions", "test_coverage", "security_notes", "diff_summary", "created_at",
		}).AddRow(
			"run-1", "review passed", "low", true, `[]`, `[]`, "covered", "",
			`{"files_changed":2,"insertions":15,"deletions":3,"files":[{"path":"a.go","status":"modified","insertions":10,"deletions":1,"is_test":false,"is_config":false,"is_migration":false}]}`,
			now,
		))

	mock.ExpectQuery("SELECT run_id, id, number, title, url, state, draft, created_at").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"run_id", "id", "number", "title", "url", "state", "draft", "created_at",
		}).AddRow("run-1", "pr-1", 42, "feat: candidate", "https://example.test/pr/42", "open", false, now))

	mock.ExpectQuery("SELECT a.agent_run_id, a.id, a.artifact_type, a.file_name").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"agent_run_id", "id", "artifact_type", "file_name", "mime_type", "size_bytes", "metadata", "created_at",
		}).AddRow("run-1", "artifact-1", "test_log", "tests.log", "text/plain", 1200, `{"kind":"verification"}`, now))

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/execution-evidence", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskID", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.ListTaskExecutionEvidence(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var response TaskExecutionEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	run1, ok := response.Runs["run-1"]
	if !ok {
		t.Fatal("run-1 evidence missing")
	}
	if run1.CommitHash != "abc123" {
		t.Fatalf("commit hash = %q, want abc123", run1.CommitHash)
	}
	if run1.Verification == nil || !run1.Verification.Passed || run1.Verification.Total != 18 || run1.Verification.Skipped != 1 {
		t.Fatalf("verification = %#v", run1.Verification)
	}
	if run1.Review == nil || !run1.Review.Approvable || run1.Review.DiffSummary.FilesChanged != 2 {
		t.Fatalf("review = %#v", run1.Review)
	}
	if run1.PullRequest == nil || run1.PullRequest.Number != 42 || run1.PullRequest.State != "open" {
		t.Fatalf("pull request = %#v", run1.PullRequest)
	}
	if len(run1.Artifacts) != 1 || run1.Artifacts[0].ID != "artifact-1" {
		t.Fatalf("artifacts = %#v", run1.Artifacts)
	}
	run2 := response.Runs["run-2"]
	if run2.Failure == nil || run2.Failure.Message != "verification rejected candidate" {
		t.Fatalf("failure = %#v", run2.Failure)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled expectations: %v", err)
	}
}
