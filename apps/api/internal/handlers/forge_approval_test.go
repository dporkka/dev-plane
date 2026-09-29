package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/api/internal/forgeexec"
	"github.com/ai-dev-control-plane/api/internal/forgereplay"
	"github.com/ai-dev-control-plane/models"
)

func approvalRow(id string, response any, expiresAt any) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "response", "expires_at"}).
		AddRow(id, response, expiresAt)
}

func branchCreateBody() string {
	return `{"request_id":"req-approval-00000001","task_id":"task-1","run_id":"run-1","command":{"type":"create_branch","repository":{"owner":"nulang-org","name":"nulang"},"name":"agent/task-1","from":"main"}}`
}

func TestExecuteForgeWorkloadCreatesStableApprovalAndReleasesClaim(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))
	mock.ExpectQuery("SELECT id, response, expires_at FROM approvals WHERE forge_request_id = \\$1").
		WithArgs("req-approval-00000001").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO approvals").
		WithArgs(sqlmock.AnyArg(), "task-1", "run-1", models.ApprovalTypeForgeCapability,
			"user-1", sqlmock.AnyArg(), sqlmock.AnyArg(), "req-approval-00000001").
		WillReturnResult(sqlmock.NewResult(1, 1))

	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, branchCreateBody()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "approval_required" || body["approval_id"] == "" {
		t.Fatalf("response = %#v", body)
	}
	if replay.releasedID != "req-approval-00000001" {
		t.Fatalf("released id = %q", replay.releasedID)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor calls = %d, want 0", len(executor.calls))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteForgeWorkloadPendingApprovalIsReusedWithoutDuplication(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))
	mock.ExpectQuery("SELECT id, response, expires_at FROM approvals WHERE forge_request_id = \\$1").
		WithArgs("req-approval-00000001").
		WillReturnRows(approvalRow("approval-1", nil, nil))

	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, branchCreateBody()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["approval_id"] != "approval-1" {
		t.Fatalf("approval id = %#v", body["approval_id"])
	}
	if replay.releasedID != "req-approval-00000001" {
		t.Fatalf("released id = %q", replay.releasedID)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor calls = %d", len(executor.calls))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteForgeWorkloadApprovedRequestContinuesSameExecutionIdentity(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{response: forgeexec.Response{
		Type: "branch",
		Value: forgeexec.BranchRef{Name: "agent/task-1", CommitID: "deadbeef"},
	}}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))
	mock.ExpectQuery("SELECT id, response, expires_at FROM approvals WHERE forge_request_id = \\$1").
		WithArgs("req-approval-00000001").
		WillReturnRows(approvalRow("approval-1", models.ApprovalResponseApproved, nil))

	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, branchCreateBody()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(executor.calls) != 1 || executor.calls[0].Type != "create_branch" {
		t.Fatalf("executor calls = %#v", executor.calls)
	}
	if replay.completedID != "req-approval-00000001" {
		t.Fatalf("completed id = %q", replay.completedID)
	}
	if replay.releasedID != "" {
		t.Fatalf("approved execution unexpectedly released claim %q", replay.releasedID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteForgeWorkloadRejectedApprovalIsTerminal(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))
	mock.ExpectQuery("SELECT id, response, expires_at FROM approvals WHERE forge_request_id = \\$1").
		WithArgs("req-approval-00000001").
		WillReturnRows(approvalRow("approval-1", models.ApprovalResponseRejected, nil))

	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, branchCreateBody()))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor calls = %d", len(executor.calls))
	}
	if replay.completedID != "req-approval-00000001" || replay.completedCode != http.StatusForbidden {
		t.Fatalf("completion = (%q,%d)", replay.completedID, replay.completedCode)
	}
	if replay.releasedID != "" {
		t.Fatalf("rejected request should be terminal, released %q", replay.releasedID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteForgeWorkloadExpiredApprovalIsTerminal(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))
	mock.ExpectQuery("SELECT id, response, expires_at FROM approvals WHERE forge_request_id = \\$1").
		WithArgs("req-approval-00000001").
		WillReturnRows(approvalRow("approval-1", nil, time.Unix(1, 0)))

	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, branchCreateBody()))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if replay.completedID != "req-approval-00000001" {
		t.Fatalf("completed id = %q", replay.completedID)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor calls = %d", len(executor.calls))
	}
}

func TestExecuteForgeWorkloadApprovalCreationRaceReusesWinner(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))
	mock.ExpectQuery("SELECT id, response, expires_at FROM approvals WHERE forge_request_id = \\$1").
		WithArgs("req-approval-00000001").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO approvals").
		WithArgs(sqlmock.AnyArg(), "task-1", "run-1", models.ApprovalTypeForgeCapability,
			"user-1", sqlmock.AnyArg(), sqlmock.AnyArg(), "req-approval-00000001").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT id, response, expires_at FROM approvals WHERE forge_request_id = \\$1").
		WithArgs("req-approval-00000001").
		WillReturnRows(approvalRow("approval-winner", nil, nil))

	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, branchCreateBody()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["approval_id"] != "approval-winner" {
		t.Fatalf("approval id = %#v", body["approval_id"])
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor calls = %d", len(executor.calls))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
