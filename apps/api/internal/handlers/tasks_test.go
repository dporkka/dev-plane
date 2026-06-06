package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/events"
)

var taskCols = []string{
	"id", "project_id", "repository_id", "workspace_id", "created_by", "source", "source_id",
	"title", "description", "status", "priority", "risk_level", "target_branch",
	"spec", "acceptance_criteria", "max_cost", "max_runtime_minutes",
	"approval_requirements", "metadata", "started_at", "completed_at", "created_at", "updated_at",
}

func taskRow(id, projectID, repoID, title, status string, createdAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(taskCols).
		AddRow(id, projectID, repoID, nil, "user-1", "web", nil,
			title, nil, status, "medium", "low", "main",
			nil, nil, nil, 60,
			nil, nil, nil, nil, createdAt, createdAt)
}

func TestListTasks(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	projectID := "proj-1"
	now := time.Now()
	rows := sqlmock.NewRows(taskCols).
		AddRow("task-1", projectID, "repo-1", nil, "user-1", "web", nil,
			"Task One", nil, "backlog", "medium", "low", "main",
			nil, nil, nil, 60,
			nil, nil, nil, nil, now, now).
		AddRow("task-2", projectID, "repo-1", nil, "user-1", "web", nil,
			"Task Two", nil, "running", "high", "medium", "main",
			nil, nil, nil, 60,
			nil, nil, nil, nil, now, now)

	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id").
		WithArgs(projectID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/tasks", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", projectID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ListTasks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var tasks []Task
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	if tasks[0].Title != "Task One" {
		t.Errorf("expected first task title 'Task One', got %q", tasks[0].Title)
	}

	if tasks[1].Status != "running" {
		t.Errorf("expected second task status 'running', got %q", tasks[1].Status)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestListTasks_Empty(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	projectID := "proj-1"
	rows := sqlmock.NewRows(taskCols)

	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id").
		WithArgs(projectID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/tasks", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", projectID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ListTasks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var tasks []Task
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if tasks == nil || len(tasks) != 0 {
		t.Fatalf("expected empty tasks, got %v", tasks)
	}
}

func TestListTasks_DBError(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	projectID := "proj-1"
	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id").
		WithArgs(projectID).
		WillReturnError(errors.New("connection failed"))

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/tasks", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", projectID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ListTasks(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCreateTask(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	projectID := "proj-1"
	userID := "user-1"

	mock.ExpectExec("INSERT INTO tasks").
		WithArgs(sqlmock.AnyArg(), projectID, "repo-1", userID, "web", nil, "Build Feature", sqlmock.AnyArg(), "medium", "low", "main", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	body, _ := json.Marshal(CreateTaskRequest{
		RepositoryID: "repo-1",
		Title:        "Build Feature",
	})
	req := httptest.NewRequest(http.MethodPost, "/projects/"+projectID+"/tasks", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: userID}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", projectID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CreateTask(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}

	var task Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if task.Title != "Build Feature" {
		t.Errorf("expected title 'Build Feature', got %q", task.Title)
	}

	if task.ProjectID != projectID {
		t.Errorf("expected project_id %q, got %q", projectID, task.ProjectID)
	}

	if task.Status != "backlog" {
		t.Errorf("expected status 'backlog', got %q", task.Status)
	}

	if task.Priority != "medium" {
		t.Errorf("expected default priority 'medium', got %q", task.Priority)
	}

	if task.RiskLevel != "low" {
		t.Errorf("expected default risk_level 'low', got %q", task.RiskLevel)
	}

	if task.TargetBranch != "main" {
		t.Errorf("expected default target_branch 'main', got %q", task.TargetBranch)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCreateTask_MissingTitle(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	projectID := "proj-1"
	body, _ := json.Marshal(CreateTaskRequest{RepositoryID: "repo-1"})
	req := httptest.NewRequest(http.MethodPost, "/projects/"+projectID+"/tasks", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "user-1"}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", projectID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CreateTask(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestCreateTask_MissingProject(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	// Missing projectID in URL
	body, _ := json.Marshal(CreateTaskRequest{RepositoryID: "repo-1", Title: "Build Feature"})
	req := httptest.NewRequest(http.MethodPost, "/projects//tasks", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "user-1"}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CreateTask(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestCreateTask_InvalidStatus(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	projectID := "proj-1"
	userID := "user-1"

	// The handler doesn't validate status on create; it always sets backlog.
	// But we can test that invalid JSON body is rejected.
	body := []byte(`{invalid json`)
	req := httptest.NewRequest(http.MethodPost, "/projects/"+projectID+"/tasks", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: userID}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", projectID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CreateTask(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetTask(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	projectID := "proj-1"
	now := time.Now()
	rows := taskRow(taskID, projectID, "repo-1", "Task One", "backlog", now)

	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id").
		WithArgs(taskID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.GetTask(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var task Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if task.ID != taskID {
		t.Errorf("expected task ID %q, got %q", taskID, task.ID)
	}

	if task.Title != "Task One" {
		t.Errorf("expected title 'Task One', got %q", task.Title)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "nonexistent"
	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id").
		WithArgs(taskID).
		WillReturnError(sql.ErrNoRows)

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.GetTask(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestUpdateTask(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	projectID := "proj-1"
	now := time.Now()
	newTitle := "Updated Title"

	// First query: get current status
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("backlog"))

	// Update query
	mock.ExpectExec("UPDATE tasks SET").
		WithArgs(&newTitle, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// GetTask query after update
	rows := taskRow(taskID, projectID, "repo-1", "Updated Title", "backlog", now)
	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id").
		WithArgs(taskID).
		WillReturnRows(rows)

	body, _ := json.Marshal(UpdateTaskRequest{Title: &newTitle})
	req := httptest.NewRequest(http.MethodPut, "/tasks/"+taskID, bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.UpdateTask(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var task Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if task.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got %q", task.Title)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestUpdateTask_InvalidStatus(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("backlog"))

	invalidStatus := "invalid_status"
	body, _ := json.Marshal(UpdateTaskRequest{Status: &invalidStatus})
	req := httptest.NewRequest(http.MethodPut, "/tasks/"+taskID, bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.UpdateTask(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestUpdateTask_InvalidTransition(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("backlog"))

	// backlog -> done is not a valid transition
	newStatus := "done"
	body, _ := json.Marshal(UpdateTaskRequest{Status: &newStatus})
	req := httptest.NewRequest(http.MethodPut, "/tasks/"+taskID, bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.UpdateTask(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestApproveSpec(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	publisher := &fakeEventPublisher{}
	h.WithEventPublisher(publisher)

	taskID := "task-1"

	mock.ExpectExec("UPDATE tasks SET status = 'approved'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/approve-spec", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ApproveSpec(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "approved" {
		t.Errorf("expected status 'approved', got %q", resp["status"])
	}
	if publisher.subject != events.TaskApproved {
		t.Fatalf("published subject = %q, want %s", publisher.subject, events.TaskApproved)
	}
	var event events.TaskEvent
	if err := json.Unmarshal(publisher.data, &event); err != nil {
		t.Fatalf("unmarshal task approved event: %v", err)
	}
	if event.TaskID != taskID || event.Status != "approved" {
		t.Fatalf("task approved event = %+v", event)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestApproveSpec_WrongStatus(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	mock.ExpectExec("UPDATE tasks SET status = 'approved'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 0))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/approve-spec", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ApproveSpec(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCancelTask(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	mock.ExpectExec("UPDATE tasks SET status = 'cancelled'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/cancel", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CancelTask(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "cancelled" {
		t.Errorf("expected status 'cancelled', got %q", resp["status"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCancelTask_AlreadyDone(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	mock.ExpectExec("UPDATE tasks SET status = 'cancelled'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 0))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/cancel", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CancelTask(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
