package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

// A failing database cursor must not be mistaken for an empty workspace list.
func TestListTaskWorkspaces_RowCloseFailureFailsClosed(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	taskID := "task-rows-fail"
	expectAuthorizeTask(mock, taskID)
	columns := []string{
		"id", "repository_id", "task_id", "name", "branch", "base_branch",
		"worktree_path", "runtime_provider", "runtime_session_id", "status", "preview_url",
		"settings", "created_at", "updated_at", "deleted_at",
	}
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	rows := sqlmock.NewRows(columns).
		AddRow("ws-a", "repo-a", taskID, "A", "main", "main", nil, "local", nil, "ready", nil, nil, now, now, nil).
		CloseError(errors.New("simulated cursor close failure"))
	mock.ExpectQuery("SELECT id, repository_id, task_id, name, branch").
		WithArgs(taskID).WillReturnRows(rows)
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/workspaces", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskID", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(withTestUser(req.Context()))
	rec := httptest.NewRecorder()
	h.ListTaskWorkspaces(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("database cursor error must not return status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
