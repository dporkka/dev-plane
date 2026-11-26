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

// A database driver can fail during row iteration after QueryContext succeeds.
// In that case ListTasks must not return a misleading successful partial list.
func TestListTasks_IterationErrorFailsClosed(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	const projectID = "proj-1"
	expectAuthorizeProject(mock, projectID)
	rows := sqlmock.NewRows(taskCols).
		AddRow("task-1", projectID, "repo-1", nil, "user-1", "web", nil,
			"Task One", nil, "backlog", "medium", "low", "main",
			nil, nil, nil, 60,
			nil, nil, nil, nil, time.Now(), time.Now()).
		RowError(0, errors.New("row decoding interrupted"))
	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id").
		WithArgs(projectID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/tasks", nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("projectID", projectID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, ctx)))
	rec := httptest.NewRecorder()
	h.ListTasks(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("iteration error status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
