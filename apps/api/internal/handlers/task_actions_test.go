package handlers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/events"
)

func TestGenerateSpec(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	publisher := &fakeEventPublisher{}
	h.WithEventPublisher(publisher)

	taskID := "task-1"
	repoID := "repo-1"

	expectAuthorizeTask(mock, taskID)
	// Check current status
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("backlog"))

	expectSpecGeneration(mock, taskID, repoID, "Build API endpoint", "backlog")

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/generate-spec", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
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
	if resp["message"] != "Spec generated" {
		t.Errorf("expected generated message, got %q", resp["message"])
	}
	if resp["spec_id"] == "" {
		t.Fatal("expected spec_id in response")
	}
	if publisher.subject != events.TaskUpdated {
		t.Fatalf("published subject = %q, want %s", publisher.subject, events.TaskUpdated)
	}
	var event events.TaskEvent
	if err := json.Unmarshal(publisher.data, &event); err != nil {
		t.Fatalf("unmarshal task updated event: %v", err)
	}
	if event.TaskID != taskID || event.Status != "spec_review" {
		t.Fatalf("task updated event = %+v", event)
	}
	var eventData map[string]string
	if err := json.Unmarshal(event.Data, &eventData); err != nil {
		t.Fatalf("unmarshal task updated data: %v", err)
	}
	if eventData["action"] != "spec_generated" || eventData["spec_id"] == "" {
		t.Fatalf("task updated data = %+v", eventData)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGenerateSpec_FromFailed(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	repoID := "repo-1"

	expectAuthorizeTask(mock, taskID)
	// Check current status - failed is also allowed
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("failed"))

	expectSpecGeneration(mock, taskID, repoID, "Fix login bug", "failed")

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/generate-spec", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
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

func expectSpecGeneration(mock sqlmock.Sqlmock, taskID, repoID, title, status string) {
	now := time.Now().UTC()
	taskCols := []string{
		"id", "project_id", "repository_id", "workspace_id", "created_by",
		"source", "source_id", "title", "description", "status", "priority",
		"risk_level", "target_branch", "spec", "acceptance_criteria",
		"max_cost", "max_runtime_minutes", "approval_requirements", "metadata",
		"started_at", "completed_at", "created_at", "updated_at", "deleted_at",
	}
	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id, created_by").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows(taskCols).AddRow(
			taskID, "proj-1", repoID, nil, "user-1",
			"web", nil, title, "Implement the requested behavior", status, "medium",
			"low", "main", nil, "[]",
			nil, 60, "[]", "{}",
			nil, nil, now, now, nil,
		))
	mock.ExpectQuery("SELECT id, project_id, github_id, owner, name, full_name, clone_url").
		WithArgs(repoID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "github_id", "owner", "name", "full_name", "clone_url",
			"default_branch", "private", "connection_status", "last_synced_at",
			"webhook_secret", "settings", "created_at", "updated_at",
		}))
	mock.ExpectQuery("SELECT id, repository_id, package_manager, framework, test_command").
		WithArgs(repoID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "repository_id", "package_manager", "framework", "test_command",
			"lint_command", "typecheck_command", "dev_command", "build_command",
			"has_dockerfile", "has_devcontainer", "detected_at", "updated_at",
		}))
	mock.ExpectExec("INSERT INTO task_specs").
		WithArgs(
			sqlmock.AnyArg(), taskID, sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), "template-heuristic", sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE tasks SET status =").
		WithArgs(taskID, "spec_review", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

func TestGenerateSpec_WrongStatus(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	expectAuthorizeTask(mock, taskID)
	// Check current status - running is not allowed
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("running"))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/generate-spec", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
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

	expectAuthorizeTask(mock, taskID)
	// Get task details - must be approved
	mock.ExpectQuery("SELECT status, project_id, repository_id, workspace_id, target_branch, risk_level").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "project_id", "repository_id", "workspace_id", "target_branch", "risk_level"}).
			AddRow("approved", "proj-1", "repo-1", workspaceID, "main", "low"))

	expectReadyTaskReadiness(mock, taskID)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE tasks SET status = 'running'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO agent_runs").
		WithArgs(sqlmock.AnyArg(), taskID, workspaceID, admissionMetadataMatcher{}, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/start-run", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
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

func TestStartRunRejectsConcurrentInitialRunClaim(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	expectAuthorizeTask(mock, taskID)
	mock.ExpectQuery("SELECT status, project_id, repository_id, workspace_id, target_branch, risk_level").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "project_id", "repository_id", "workspace_id", "target_branch", "risk_level"}).
			AddRow("approved", "proj-1", "repo-1", nil, "main", "low"))
	expectReadyTaskReadiness(mock, taskID)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE tasks SET status = 'running'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/start-run", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.StartRun(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d: %s", http.StatusConflict, rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "initial run already claimed" {
		t.Fatalf("error = %q, want initial run already claimed", resp["error"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestStartRun_NotApproved(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"

	expectAuthorizeTask(mock, taskID)
	// Get task details - not approved
	mock.ExpectQuery("SELECT status, project_id, repository_id, workspace_id, target_branch, risk_level").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "project_id", "repository_id", "workspace_id", "target_branch", "risk_level"}).
			AddRow("backlog", "proj-1", "repo-1", nil, "main", "low"))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/start-run", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
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

	expectAuthorizeAgentRun(mock, runID)
	// Get failed run details
	mock.ExpectQuery("SELECT task_id, workspace_id, agent_role, model, provider, status").
		WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "workspace_id", "agent_role", "model", "provider", "status"}).
			AddRow(taskID, nil, "implementer", nil, nil, "failed"))

	// Get task status - must be failed/reviewing/pr_created
	mock.ExpectQuery("SELECT status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("failed"))
	mock.ExpectQuery("SELECT metadata FROM agent_runs").
		WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{"metadata"}).
			AddRow(`{"admission":{"policy":"task-readiness-v1","readiness":{"status":"ready","checks":[]}}}`))

	// Insert new agent run
	mock.ExpectExec("INSERT INTO agent_runs").
		WithArgs(sqlmock.AnyArg(), taskID, nil, "implementer", "gpt-4o", "openai", retryAdmissionMetadataMatcher{originalRunID: runID}, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Update task status to running
	mock.ExpectExec("UPDATE tasks SET status = 'running'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID+"/retry", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", runID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
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

	expectAuthorizeAgentRun(mock, runID)
	// Get run details - status is completed, not failed
	mock.ExpectQuery("SELECT task_id, workspace_id, agent_role, model, provider, status").
		WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "workspace_id", "agent_role", "model", "provider", "status"}).
			AddRow(taskID, nil, "implementer", nil, nil, "completed"))

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID+"/retry", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", runID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
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

	expectAuthorizeAgentRun(mock, runID)
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
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.RetryRun(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

type admissionMetadataMatcher struct{}

func (admissionMetadataMatcher) Match(value driver.Value) bool {
	var data []byte
	switch typed := value.(type) {
	case string:
		data = []byte(typed)
	case []byte:
		data = typed
	default:
		return false
	}
	var metadata struct {
		Admission struct {
			Policy    string `json:"policy"`
			Readiness struct {
				Status string `json:"status"`
			} `json:"readiness"`
		} `json:"admission"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return false
	}
	return metadata.Admission.Policy == "task-readiness-v1" &&
		metadata.Admission.Readiness.Status == "ready"
}

func expectReadyTaskReadiness(mock sqlmock.Sqlmock, taskID string) {
	mock.ExpectQuery("SELECT implementation_plan, files_to_change, files_to_create, acceptance_criteria").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"implementation_plan", "files_to_change", "files_to_create", "acceptance_criteria",
			"test_plan", "rollback_plan", "required_approvals",
		}).AddRow(
			`["implement change","add tests"]`,
			`["apps/api/handler.go"]`,
			`[]`,
			`["request succeeds"]`,
			"go test ./...",
			"",
			`[]`,
		))
}

func TestStartRunBlockedByTaskReadiness(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	expectAuthorizeTask(mock, taskID)
	mock.ExpectQuery("SELECT status, project_id, repository_id, workspace_id, target_branch, risk_level").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "project_id", "repository_id", "workspace_id", "target_branch", "risk_level",
		}).AddRow("approved", "proj-1", "repo-1", nil, "main", "low"))
	mock.ExpectQuery("SELECT implementation_plan, files_to_change, files_to_create, acceptance_criteria").
		WithArgs(taskID).
		WillReturnError(sql.ErrNoRows)

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/start-run", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.StartRun(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d: %s", http.StatusConflict, rec.Code, rec.Body.String())
	}
	var resp struct {
		Error     string `json:"error"`
		Readiness struct {
			Status string `json:"status"`
		} `json:"readiness"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != "task readiness blocked" {
		t.Fatalf("error = %q, want task readiness blocked", resp.Error)
	}
	if resp.Readiness.Status != "blocked" {
		t.Fatalf("readiness status = %q, want blocked", resp.Readiness.Status)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

type retryAdmissionMetadataMatcher struct {
	originalRunID string
}

func (m retryAdmissionMetadataMatcher) Match(value driver.Value) bool {
	var data []byte
	switch typed := value.(type) {
	case string:
		data = []byte(typed)
	case []byte:
		data = typed
	default:
		return false
	}
	var metadata struct {
		Admission struct {
			Policy string `json:"policy"`
		} `json:"admission"`
		Retry struct {
			OriginalRunID string `json:"original_run_id"`
		} `json:"retry"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return false
	}
	return metadata.Admission.Policy == "task-readiness-v1" &&
		metadata.Retry.OriginalRunID == m.originalRunID
}
