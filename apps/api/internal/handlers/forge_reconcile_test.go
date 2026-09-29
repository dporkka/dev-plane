package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/api/internal/forgeexec"
	"github.com/ai-dev-control-plane/api/internal/forgereplay"
	"github.com/ai-dev-control-plane/api/internal/workloadauth"
)

func signedForgeReconcileRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/reconcile", strings.NewReader(body))
	workloadauth.Sign(req, "nulang-cloud", forgeWorkloadSecret, time.Unix(1_800_000_000, 0), []byte(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func reconcileBody() string {
	return `{"request_id":"req-reconcile-000001","task_id":"task-1","run_id":"run-1","command":{"type":"create_branch","repository":{"owner":"nulang-org","name":"nulang"},"name":"agent/task-1","from":"main"}}`
}

func TestReconcileForgeWorkloadResolvesAppliedMutationWithoutReexecution(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	evidence := forgeexec.Evidence{
		Provider: "fake", Repository: "nulang-org/nulang", Operation: "branch.create",
		CommandType: "create_branch", Source: "reconciliation", HeadSHA: "deadbeef",
	}
	response := forgeexec.Response{Type: "branch", Value: forgeexec.BranchRef{Name: "agent/task-1", CommitID: "deadbeef"}}
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateUncertain}}
	executor := &fakeForgeExecutor{reconcileResult: forgeexec.ReconcileResult{
		Status: forgeexec.ReconcileApplied, Response: &response, Evidence: evidence,
	}}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").WillReturnRows(forgeContextRows("implementer"))

	rec := httptest.NewRecorder()
	h.ReconcileForgeWorkload(rec, signedForgeReconcileRequest(t, reconcileBody()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(executor.calls) != 0 || len(executor.reconcileCalls) != 1 {
		t.Fatalf("execute=%d reconcile=%d", len(executor.calls), len(executor.reconcileCalls))
	}
	if replay.resolvedID != "req-reconcile-000001" {
		t.Fatalf("resolved=%q", replay.resolvedID)
	}
	var got ForgeReconcileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != string(forgeexec.ReconcileApplied) || got.Retryable || got.Response == nil {
		t.Fatalf("response=%+v", got)
	}
}

func TestReconcileForgeWorkloadMakesProvenAbsentMutationRetryable(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateUncertain}}
	executor := &fakeForgeExecutor{reconcileResult: forgeexec.ReconcileResult{
		Status:   forgeexec.ReconcileNotApplied,
		Evidence: forgeexec.Evidence{Provider: "fake", Source: "reconciliation", Detail: "branch absent"},
	}}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)
	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").WillReturnRows(forgeContextRows("implementer"))

	rec := httptest.NewRecorder()
	h.ReconcileForgeWorkload(rec, signedForgeReconcileRequest(t, reconcileBody()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if replay.retryID != "req-reconcile-000001" {
		t.Fatalf("retry id=%q", replay.retryID)
	}
	var got ForgeReconcileResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Status != string(forgeexec.ReconcileNotApplied) || !got.Retryable {
		t.Fatalf("response=%+v", got)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("execute calls=%d", len(executor.calls))
	}
}

func TestReconcileForgeWorkloadKeepsAmbiguousMutationUnretryable(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateUncertain}}
	executor := &fakeForgeExecutor{reconcileResult: forgeexec.ReconcileResult{
		Status:   forgeexec.ReconcileAmbiguous,
		Evidence: forgeexec.Evidence{Provider: "fake", Source: "reconciliation", Detail: "state differs"},
		Reason:   "state differs",
	}}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)
	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").WillReturnRows(forgeContextRows("implementer"))

	rec := httptest.NewRecorder()
	h.ReconcileForgeWorkload(rec, signedForgeReconcileRequest(t, reconcileBody()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if replay.keptID != "req-reconcile-000001" {
		t.Fatalf("kept id=%q", replay.keptID)
	}
	var got ForgeReconcileResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Status != string(forgeexec.ReconcileAmbiguous) || got.Retryable {
		t.Fatalf("response=%+v", got)
	}
}

func TestReconcileForgeWorkloadMissingExecutionDoesNotCreateReplayState(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{err: forgereplay.ErrNotFound}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	rec := httptest.NewRecorder()
	h.ReconcileForgeWorkload(rec, signedForgeReconcileRequest(t, reconcileBody()))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(replay.claims) != 0 || len(replay.inspects) != 1 {
		t.Fatalf("claims=%d inspects=%d", len(replay.claims), len(replay.inspects))
	}
	if len(executor.reconcileCalls) != 0 {
		t.Fatalf("reconcile calls=%d", len(executor.reconcileCalls))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileForgeWorkloadReturnsAlreadyResolvedReplay(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	response := forgeexec.Response{Type: "branch", Value: forgeexec.BranchRef{Name: "agent/task-1", CommitID: "deadbeef"}}
	responseBody, _ := json.Marshal(response)
	evidence := forgeexec.Evidence{Provider: "fake", Source: "provider_response", HeadSHA: "deadbeef"}
	evidenceBody, _ := json.Marshal(evidence)
	replay := &fakeForgeReplayStore{result: forgereplay.Result{
		State: forgereplay.StateReplay, ResponseStatus: http.StatusOK, ResponseBody: responseBody, Evidence: evidenceBody,
	}}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	rec := httptest.NewRecorder()
	h.ReconcileForgeWorkload(rec, signedForgeReconcileRequest(t, reconcileBody()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(executor.reconcileCalls) != 0 {
		t.Fatalf("reconcile calls=%d", len(executor.reconcileCalls))
	}
	var got ForgeReconcileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "applied" || got.Response == nil || got.Evidence.HeadSHA != "deadbeef" {
		t.Fatalf("response=%+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileForgeWorkloadProviderProbeFailureLeavesUncertain(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateUncertain}}
	executor := &fakeForgeExecutor{reconcileErr: errors.New("provider unavailable")}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)
	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").WillReturnRows(forgeContextRows("implementer"))

	rec := httptest.NewRecorder()
	h.ReconcileForgeWorkload(rec, signedForgeReconcileRequest(t, reconcileBody()))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if replay.retryID != "" || replay.resolvedID != "" {
		t.Fatalf("unexpected state transition retry=%q resolved=%q", replay.retryID, replay.resolvedID)
	}
}
