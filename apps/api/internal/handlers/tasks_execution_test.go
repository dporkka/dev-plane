package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/events"
)

func TestApproveSpecPublishesExplicitAgentRuntimeSelection(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	publisher := &fakeEventPublisher{}
	h.WithEventPublisher(publisher)

	taskID := "task-1"
	expectAuthorizeTask(mock, taskID)
	mock.ExpectQuery("SELECT repository_id, risk_level, status FROM tasks").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"repository_id", "risk_level", "status"}).
			AddRow("repo-1", "low", "spec_review"))
	expectReadyTaskReadiness(mock, taskID)
	mock.ExpectExec("UPDATE tasks SET status = 'approved'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	body := []byte(`{"execution":{"backend":"agent_runtime","provider":"codex"}}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/approve-spec", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.ApproveSpec(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ApproveSpec() status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if publisher.subject != events.TaskApproved {
		t.Fatalf("published subject = %q, want %s", publisher.subject, events.TaskApproved)
	}

	var event events.TaskEvent
	if err := json.Unmarshal(publisher.data, &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	var data struct {
		Execution struct {
			Backend  string `json:"backend"`
			Provider string `json:"provider"`
		} `json:"execution"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("unmarshal event data: %v", err)
	}
	if data.Execution.Backend != "agent_runtime" || data.Execution.Provider != "codex" {
		t.Fatalf("execution = %#v", data.Execution)
	}
}

func TestApproveSpecRejectsAgentRuntimeWithoutProvider(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	expectAuthorizeTask(mock, taskID)

	body := []byte(`{"execution":{"backend":"agent_runtime"}}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/approve-spec", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.ApproveSpec(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ApproveSpec() status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
