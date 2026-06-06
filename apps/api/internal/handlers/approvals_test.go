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

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
)

func TestRespondApprovalPublishesApprovedEvent(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	publisher := &fakeEventPublisher{}
	h.WithEventPublisher(publisher)

	mock.ExpectQuery("SELECT task_id, agent_run_id, approval_type").
		WithArgs("approval-1").
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "agent_run_id", "approval_type"}).
			AddRow("task-1", "run-1", models.ApprovalTypePRCreate))
	mock.ExpectExec("UPDATE approvals").
		WithArgs("user-1", models.ApprovalResponseApproved, "ship it", sqlmock.AnyArg(), "approval-1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := approvalResponseRequest(t, "approval-1", `{"response":"approved","response_note":"ship it"}`)
	rr := httptest.NewRecorder()
	h.RespondApproval(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if publisher.subject != events.ApprovalApproved {
		t.Fatalf("published subject = %q, want %s", publisher.subject, events.ApprovalApproved)
	}
	var payload map[string]any
	if err := json.Unmarshal(publisher.data, &payload); err != nil {
		t.Fatalf("unmarshal published payload: %v", err)
	}
	if payload["approval_id"] != "approval-1" || payload["task_id"] != "task-1" || payload["agent_run_id"] != "run-1" || payload["approval_type"] != models.ApprovalTypePRCreate {
		t.Fatalf("payload = %+v", payload)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}

func TestRespondApprovalRejectedPublishesEventAndFailsTask(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	publisher := &fakeEventPublisher{}
	h.WithEventPublisher(publisher)

	mock.ExpectQuery("SELECT task_id, agent_run_id, approval_type").
		WithArgs("approval-1").
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "agent_run_id", "approval_type"}).
			AddRow("task-1", "run-1", models.ApprovalTypePRCreate))
	mock.ExpectExec("UPDATE approvals").
		WithArgs("user-1", models.ApprovalResponseRejected, "needs changes", sqlmock.AnyArg(), "approval-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE tasks SET status = 'failed'").
		WithArgs(sqlmock.AnyArg(), "task-1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := approvalResponseRequest(t, "approval-1", `{"response":"rejected","response_note":"needs changes"}`)
	rr := httptest.NewRecorder()
	h.RespondApproval(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if publisher.subject != events.ApprovalRejected {
		t.Fatalf("published subject = %q, want %s", publisher.subject, events.ApprovalRejected)
	}
	var payload map[string]any
	if err := json.Unmarshal(publisher.data, &payload); err != nil {
		t.Fatalf("unmarshal published payload: %v", err)
	}
	if payload["response"] != models.ApprovalResponseRejected || payload["note"] != "needs changes" {
		t.Fatalf("payload = %+v", payload)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}

func TestRespondApprovalAlreadyRespondedDoesNotPublish(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	publisher := &fakeEventPublisher{}
	h.WithEventPublisher(publisher)

	mock.ExpectQuery("SELECT task_id, agent_run_id, approval_type").
		WithArgs("approval-1").
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "agent_run_id", "approval_type"}).
			AddRow("task-1", "run-1", models.ApprovalTypePRCreate))
	mock.ExpectExec("UPDATE approvals").
		WithArgs("user-1", models.ApprovalResponseApproved, "", sqlmock.AnyArg(), "approval-1").
		WillReturnResult(sqlmock.NewResult(1, 0))

	req := approvalResponseRequest(t, "approval-1", `{"response":"approved"}`)
	rr := httptest.NewRecorder()
	h.RespondApproval(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if publisher.subject != "" {
		t.Fatalf("published subject = %q, want no publish", publisher.subject)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}

type fakeEventPublisher struct {
	subject string
	data    []byte
	err     error
}

func (p *fakeEventPublisher) Publish(subject string, data []byte) error {
	p.subject = subject
	p.data = append([]byte(nil), data...)
	return p.err
}

func approvalResponseRequest(t *testing.T, approvalID, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+approvalID+"/respond", bytes.NewReader([]byte(body)))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "user-1"}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", approvalID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
