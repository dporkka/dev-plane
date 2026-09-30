package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func TestGetTaskReadinessUsesSpecAndDetectedVerification(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	repositoryID := "repo-1"
	expectAuthorizeTask(mock, taskID)
	mock.ExpectQuery("SELECT repository_id, risk_level FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"repository_id", "risk_level"}).AddRow(repositoryID, "low"))
	mock.ExpectQuery("SELECT implementation_plan, files_to_change, files_to_create, acceptance_criteria").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"implementation_plan", "files_to_change", "files_to_create", "acceptance_criteria",
			"test_plan", "rollback_plan", "required_approvals",
		}).AddRow(
			`["edit handler","add tests"]`,
			`["apps/api/handler.go"]`,
			`[]`,
			`["request returns 200"]`,
			"",
			"",
			`[]`,
		))
	mock.ExpectQuery("SELECT test_command, lint_command, typecheck_command, build_command FROM project_configs").
		WithArgs(repositoryID).
		WillReturnRows(sqlmock.NewRows([]string{
			"test_command", "lint_command", "typecheck_command", "build_command",
		}).AddRow("go test ./...", "go vet ./...", "", "go build ./..."))

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/readiness", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.GetTaskReadiness(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var report struct {
		Status string `json:"status"`
		Checks []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if report.Status != "ready" {
		t.Fatalf("readiness status = %q, want ready", report.Status)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetTaskReadinessReportsMissingSpecAsBlocked(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	expectAuthorizeTask(mock, taskID)
	mock.ExpectQuery("SELECT repository_id, risk_level FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"repository_id", "risk_level"}).AddRow("repo-1", "low"))
	mock.ExpectQuery("SELECT implementation_plan, files_to_change, files_to_create, acceptance_criteria").
		WithArgs(taskID).
		WillReturnError(sql.ErrNoRows)

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/readiness", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.GetTaskReadiness(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var report struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if report.Status != "blocked" {
		t.Fatalf("readiness status = %q, want blocked", report.Status)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetTaskReadinessRejectsMalformedSpecEvidence(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	expectAuthorizeTask(mock, taskID)
	mock.ExpectQuery("SELECT repository_id, risk_level FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"repository_id", "risk_level"}).AddRow("repo-1", "low"))
	mock.ExpectQuery("SELECT implementation_plan, files_to_change, files_to_create, acceptance_criteria").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"implementation_plan", "files_to_change", "files_to_create", "acceptance_criteria",
			"test_plan", "rollback_plan", "required_approvals",
		}).AddRow(
			`["edit handler"]`,
			`["apps/api/handler.go"]`,
			`[]`,
			`{malformed`,
			"go test ./...",
			"",
			`[]`,
		))

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/readiness", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.GetTaskReadiness(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d: %s", http.StatusInternalServerError, rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
