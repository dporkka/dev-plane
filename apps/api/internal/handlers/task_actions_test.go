package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func TestGenerateSpec(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	// Check current status
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("backlog"))

	// Transition to spec_review
	mock.ExpectExec("UPDATE tasks SET status = 'spec_review'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/generate-spec", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.GenerateSpec(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "spec_review" {
		t.Errorf("expected status 'spec_review', got %q", resp["status"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGenerateSpec_FromFailed(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	// Check current status - failed is also allowed
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("failed"))

	// Transition to spec_review
	mock.ExpectExec("UPDATE tasks SET status = 'spec_review'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/generate-spec", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.GenerateSpec(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "spec_review" {
		t.Errorf("expected status 'spec_review', got %q", resp["status"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGenerateSpec_WrongStatus(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	// Check current status - running is not allowed
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("running"))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/generate-spec", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.GenerateSpec(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if resp["error"] == "" {
			t.Error("expected error message in response")
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestStartRun(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	workspaceID := "ws-1"

	// Get task details - must be approved
	mock.ExpectQuery("SELECT status, project_id, repository_id, workspace_id, target_branch").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "project_id", "repository_id", "workspace_id", "target_branch"}).
			AddRow("approved", "proj-1", "repo-1", workspaceID, "main"))

	// Insert agent run
	mock.ExpectExec("INSERT INTO agent_runs").
		WithArgs(sqlmock.AnyArg(), taskID, workspaceID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Update task status to running
	mock.ExpectExec("UPDATE tasks SET status = 'running'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/start-run", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.StartRun(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "queued" {
		t.Errorf("expected status 'queued', got %q", resp["status"])
	}

	if resp["run_id"] == "" {
		t.Error("expected run_id in response")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestStartRun_NotApproved(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	// Get task details - not approved
	mock.ExpectQuery("SELECT status, project_id, repository_id, workspace_id, target_branch").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "project_id", "repository_id", "workspace_id", "target_branch"}).
			AddRow("backlog", "proj-1", "repo-1", nil, "main"))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/start-run", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.StartRun(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestRetryRun(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	runID := "run-1"
	taskID := "task-1"

	// Get failed run details
	mock.ExpectQuery("SELECT task_id, workspace_id, agent_role, model, provider, status").
		WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "workspace_id", "agent_role", "model", "provider", "status"}).
			AddRow(taskID, nil, "implementer", nil, nil, "failed"))

	// Get task status - must be failed/reviewing/pr_created
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("failed"))

	// Insert new agent run
	mock.ExpectExec("INSERT INTO agent_runs").
		WithArgs(sqlmock.AnyArg(), taskID, nil, "implementer", "gpt-4o", "openai", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Update task status to running
	mock.ExpectExec("UPDATE tasks SET status = 'running'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID+"/retry", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", runID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.RetryRun(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "queued" {
		t.Errorf("expected status 'queued', got %q", resp["status"])
	}

	if resp["run_id"] == "" {
		t.Error("expected run_id in response")
	}

	if resp["original_run_id"] != runID {
		t.Errorf("expected original_run_id %q, got %q", runID, resp["original_run_id"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestRetryRun_NotFailed(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	runID := "run-1"
	taskID := "task-1"

	// Get run details - status is completed, not failed
	mock.ExpectQuery("SELECT task_id, workspace_id, agent_role, model, provider, status").
		WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "workspace_id", "agent_role", "model", "provider", "status"}).
			AddRow(taskID, nil, "implementer", nil, nil, "completed"))

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID+"/retry", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", runID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.RetryRun(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if resp["error"] == "" {
			t.Error("expected error message in response")
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestRetryRun_TaskNotRetryable(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	runID := "run-1"
	taskID := "task-1"

	// Get failed run details
	mock.ExpectQuery("SELECT task_id, workspace_id, agent_role, model, provider, status").
		WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "workspace_id", "agent_role", "model", "provider", "status"}).
			AddRow(taskID, nil, "implementer", nil, nil, "failed"))

	// Get task status - backlog does not allow retry
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("backlog"))

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID+"/retry", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", runID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.RetryRun(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
