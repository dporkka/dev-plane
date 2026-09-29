package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/forgeexec"
	"github.com/ai-dev-control-plane/api/internal/forgereplay"
	"github.com/ai-dev-control-plane/api/internal/workloadauth"
	"github.com/ai-dev-control-plane/policies"
)

type fakeForgeExecutor struct {
	response forgeexec.Response
	err      error
	calls    []forgeexec.Command
}

func (f *fakeForgeExecutor) Execute(_ context.Context, command forgeexec.Command) (forgeexec.Response, error) {
	f.calls = append(f.calls, command)
	return f.response, f.err
}

func signedForgeExecuteRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/execute", strings.NewReader(body))
	workloadauth.Sign(req, "nulang-cloud", forgeWorkloadSecret, time.Unix(1_800_000_000, 0), []byte(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestExecuteForgeWorkloadDerivesOperationAndExecutesCanonicalRepository(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{response: forgeexec.Response{
		Type: "file",
		Value: forgeexec.FileContent{Path: "README.md", Content: []int{104, 105}, SHA: "abc"},
	}}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))

	body := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","command":{"type":"read_file","repository":{"owner":"nulang-org","name":"nulang"},"path":"README.md","reference":"main"}}`
	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(executor.calls) != 1 {
		t.Fatalf("executor calls = %d", len(executor.calls))
	}
	if replay.claims[0].Operation != "repo.read" {
		t.Fatalf("claimed operation = %q", replay.claims[0].Operation)
	}
	if replay.completedID != "req-0000000000000001" {
		t.Fatalf("completed id = %q", replay.completedID)
	}
}

func TestExecuteForgeWorkloadRejectsCallerSuppliedOperation(t *testing.T) {
	h, _, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	body := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","operation":"repo.read","command":{"type":"merge_change","repository":{"owner":"nulang-org","name":"nulang"},"number":1,"method":"squash"}}`
	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, body))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(replay.claims) != 0 || len(executor.calls) != 0 {
		t.Fatal("caller-supplied operation must fail before claim or execution")
	}
}

func TestExecuteForgeWorkloadRejectsRepositoryMismatchBeforeProvider(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))

	body := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","command":{"type":"read_file","repository":{"owner":"evil","name":"other"},"path":"README.md"}}`
	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, body))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor calls = %d, want 0", len(executor.calls))
	}
}

func TestExecuteForgeWorkloadMutationFailureBecomesUncertain(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{err: errors.New("provider connection reset")}
	allowBranch := policies.NewEngine([]policies.Policy{{
		Name: "test_allow_branch_create", ResourceType: "git", Action: "create_branch",
		Effect: policies.EffectAllow, Priority: 100,
	}})
	h.WithForgeReplayStore(replay).
		WithForgeExecutor(executor).
		WithCapabilityKernel(capability.NewKernel(allowBranch, nil, nil, slog.Default()))

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))

	body := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","command":{"type":"create_branch","repository":{"owner":"nulang-org","name":"nulang"},"name":"agent/task-1","from":"main"}}`
	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, body))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if replay.uncertainID != "req-0000000000000001" {
		t.Fatalf("uncertain id = %q", replay.uncertainID)
	}
	if replay.releasedID != "" {
		t.Fatalf("mutation failure must not release claim, released=%q", replay.releasedID)
	}
}

func TestExecuteForgeWorkloadReadFailureReleasesForRetry(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	executor := &fakeForgeExecutor{err: errors.New("provider unavailable")}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))

	body := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","command":{"type":"read_file","repository":{"owner":"nulang-org","name":"nulang"},"path":"README.md"}}`
	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, body))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if replay.releasedID != "req-0000000000000001" {
		t.Fatalf("released id = %q", replay.releasedID)
	}
	if replay.uncertainID != "" {
		t.Fatalf("read failure should not be uncertain, got %q", replay.uncertainID)
	}
}

func TestExecuteForgeWorkloadReplaysCompletedResultWithoutProvider(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	cached, _ := json.Marshal(forgeexec.Response{Type: "checks", Value: []forgeexec.CheckRun{}})
	replay := &fakeForgeReplayStore{result: forgereplay.Result{
		State: forgereplay.StateReplay, ResponseStatus: http.StatusOK, ResponseBody: cached,
	}}
	executor := &fakeForgeExecutor{}
	h.WithForgeReplayStore(replay).WithForgeExecutor(executor)

	body := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","command":{"type":"list_checks","repository":{"owner":"nulang-org","name":"nulang"},"reference":"main"}}`
	rec := httptest.NewRecorder()
	h.ExecuteForgeWorkload(rec, signedForgeExecuteRequest(t, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor calls = %d, want 0", len(executor.calls))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
