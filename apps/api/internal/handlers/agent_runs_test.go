package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

var agentRunCols = []string{
	"id", "task_id", "parent_run_id", "workspace_id", "attempt", "agent_role", "model", "provider", "status", "state_version",
	"outcome", "execution_snapshot", "execution_snapshot_digest",
	"started_at", "completed_at", "prompt_tokens", "completion_tokens",
	"total_cost", "error_message", "summary", "metadata", "created_at", "updated_at",
}

func agentRunRow(id, taskID, role, status string, createdAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(agentRunCols).
		AddRow(id, taskID, nil, nil, 1, role, nil, nil, status, 1,
			"passed", "{}", "sha256:test",
			nil, nil, 0, 0,
			0.0, nil, nil, nil, createdAt, createdAt)
}

func TestListAgentRuns(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	now := time.Now()

	expectAuthorizeTask(mock, taskID)
	rows := sqlmock.NewRows(agentRunCols).
		AddRow("run-1", taskID, nil, nil, 1, "implementer", nil, nil, "completed", 3,
			"passed", "{}", "sha256:run-1",
			nil, nil, 100, 50,
			0.05, nil, nil, nil, now, now).
		AddRow("run-2", taskID, "run-1", nil, 2, "reviewer", nil, nil, "running", 2,
			nil, "{}", "sha256:run-2",
			nil, nil, 0, 0,
			0.0, nil, nil, nil, now, now)

	mock.ExpectQuery("SELECT id, task_id, parent_run_id, workspace_id, attempt, agent_role, model, provider, status").
		WithArgs(taskID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/runs", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskID", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.ListAgentRuns(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var runs []AgentRun
	if err := json.Unmarshal(rec.Body.Bytes(), &runs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	if runs[0].ID != "run-1" {
		t.Errorf("expected first run ID 'run-1', got %q", runs[0].ID)
	}

	if runs[0].AgentRole != "implementer" {
		t.Errorf("expected first run role 'implementer', got %q", runs[0].AgentRole)
	}

	if runs[0].Status != "completed" {
		t.Errorf("expected first run status 'completed', got %q", runs[0].Status)
	}

	if runs[0].PromptTokens != 100 {
		t.Errorf("expected first run prompt_tokens 100, got %d", runs[0].PromptTokens)
	}
	if runs[0].Outcome == nil || *runs[0].Outcome != "passed" {
		t.Errorf("expected first run outcome passed, got %v", runs[0].Outcome)
	}
	if runs[1].ParentRunID == nil || *runs[1].ParentRunID != "run-1" || runs[1].Attempt != 2 {
		t.Errorf("expected second run lineage parent=run-1 attempt=2, got parent=%v attempt=%d", runs[1].ParentRunID, runs[1].Attempt)
	}

	if runs[1].ID != "run-2" {
		t.Errorf("expected second run ID 'run-2', got %q", runs[1].ID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetAgentRun(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	runID := "run-1"
	taskID := "task-1"
	now := time.Now()
	rows := agentRunRow(runID, taskID, "implementer", "completed", now)

	expectAuthorizeAgentRun(mock, runID)
	mock.ExpectQuery("SELECT id, task_id, parent_run_id, workspace_id, attempt, agent_role, model, provider, status").
		WithArgs(runID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", runID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.GetAgentRun(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var run AgentRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if run.ID != runID {
		t.Errorf("expected run ID %q, got %q", runID, run.ID)
	}

	if run.TaskID != taskID {
		t.Errorf("expected task ID %q, got %q", taskID, run.TaskID)
	}

	if run.AgentRole != "implementer" {
		t.Errorf("expected agent role 'implementer', got %q", run.AgentRole)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetAgentRun_NotFound(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	runID := "nonexistent"
	expectAuthorizeAgentRun(mock, runID)
	mock.ExpectQuery("SELECT id, task_id, parent_run_id, workspace_id, attempt, agent_role, model, provider, status").
		WithArgs(runID).
		WillReturnError(sql.ErrNoRows)

	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", runID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.GetAgentRun(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestListAgentSteps(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	runID := "run-1"
	now := time.Now()

	expectAuthorizeAgentRun(mock, runID)
	rows := sqlmock.NewRows([]string{
		"id", "agent_run_id", "step_number", "step_type", "status", "outcome", "input", "output", "content",
		"tool_name", "tool_input", "tool_output", "command", "command_output",
		"exit_code", "file_path", "diff", "cost", "latency_ms", "started_at", "completed_at", "created_at",
	}).
		AddRow("step-1", runID, 1, "tool_call", "completed", "passed", `{"path":"README.md"}`, `{"content":"ok"}`, nil,
			nil, nil, nil, nil, nil,
			nil, nil, nil, 0.01, 100, now, now, now).
		AddRow("step-2", runID, 2, "command", "completed", "passed", nil, nil, nil,
			nil, nil, nil, "ls -la", "output",
			0, nil, nil, 0.0, 50, now, now, now)

	mock.ExpectQuery("SELECT id, agent_run_id, step_number, step_type, status, outcome, input, output, content").
		WithArgs(runID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/steps", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", runID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.ListAgentSteps(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var steps []AgentStep
	if err := json.Unmarshal(rec.Body.Bytes(), &steps); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}

	if steps[0].ID != "step-1" {
		t.Errorf("expected first step ID 'step-1', got %q", steps[0].ID)
	}

	if steps[0].StepNumber != 1 {
		t.Errorf("expected first step number 1, got %d", steps[0].StepNumber)
	}

	if steps[0].StepType != "tool_call" {
		t.Errorf("expected first step type 'tool_call', got %q", steps[0].StepType)
	}
	if steps[0].Outcome == nil || *steps[0].Outcome != "passed" {
		t.Errorf("expected first step outcome passed, got %v", steps[0].Outcome)
	}
	if string(steps[0].Input) != `{"path":"README.md"}` || string(steps[0].Output) != `{"content":"ok"}` {
		t.Errorf("unexpected execution envelope input=%s output=%s", steps[0].Input, steps[0].Output)
	}

	if steps[1].ID != "step-2" {
		t.Errorf("expected second step ID 'step-2', got %q", steps[1].ID)
	}

	if steps[1].StepType != "command" {
		t.Errorf("expected second step type 'command', got %q", steps[1].StepType)
	}

	if steps[1].Command == nil || *steps[1].Command != "ls -la" {
		t.Errorf("expected second step command 'ls -la', got %v", steps[1].Command)
	}

	if steps[1].ExitCode == nil || *steps[1].ExitCode != 0 {
		t.Errorf("expected second step exit code 0, got %v", steps[1].ExitCode)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
