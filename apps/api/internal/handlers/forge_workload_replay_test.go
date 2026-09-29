package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/forgereplay"
)

type fakeForgeReplayStore struct {
	result        forgereplay.Result
	err           error
	claims        []forgereplay.Request
	completedID   string
	completedCode int
	completedBody []byte
	completeErr   error
}

func (f *fakeForgeReplayStore) Claim(_ context.Context, req forgereplay.Request) (forgereplay.Result, error) {
	f.claims = append(f.claims, req)
	return f.result, f.err
}

func (f *fakeForgeReplayStore) Complete(_ context.Context, requestID string, status int, body []byte) error {
	f.completedID = requestID
	f.completedCode = status
	f.completedBody = append([]byte(nil), body...)
	return f.completeErr
}

func (f *fakeForgeReplayStore) MarkUncertain(_ context.Context, _ string, _ []byte) error {
	return nil
}

func TestAuthorizeForgeWorkloadReplaysCompletedRequestWithoutDatabaseLookup(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()

	cached := ForgeAuthorizeResponse{
		Allowed:    true,
		Effect:     "allow",
		RiskLevel:  "low",
		Repository: ForgeRepositoryResponse{ID: "repo-1", FullName: "nulang-org/nulang"},
	}
	body, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	replay := &fakeForgeReplayStore{result: forgereplay.Result{
		State:          forgereplay.StateReplay,
		ResponseStatus: http.StatusOK,
		ResponseBody:   body,
	}}
	h.WithForgeReplayStore(replay)

	requestBody := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","operation":"repo.read"}`
	rec := httptest.NewRecorder()
	h.AuthorizeForgeWorkload(rec, signedForgeRequest(t, requestBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(replay.claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(replay.claims))
	}
	if replay.claims[0].ID != "req-0000000000000001" {
		t.Fatalf("request id = %q", replay.claims[0].ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeForgeWorkloadRejectsConflictingRequestIDBeforeDatabaseLookup(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{err: forgereplay.ErrConflict}
	h.WithForgeReplayStore(replay)

	requestBody := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","operation":"repo.read"}`
	rec := httptest.NewRecorder()
	h.AuthorizeForgeWorkload(rec, signedForgeRequest(t, requestBody))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeForgeWorkloadRejectsInFlightAndUncertainReplay(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state forgereplay.State
	}{
		{"in flight", forgereplay.StateInFlight},
		{"uncertain", forgereplay.StateUncertain},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, mock, _, cleanup := forgeHandler(t)
			defer cleanup()
			h.WithForgeReplayStore(&fakeForgeReplayStore{result: forgereplay.Result{State: tt.state}})

			requestBody := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","operation":"repo.read"}`
			rec := httptest.NewRecorder()
			h.AuthorizeForgeWorkload(rec, signedForgeRequest(t, requestBody))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthorizeForgeWorkloadPersistsDecisionBeforeReturningAllow(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{result: forgereplay.Result{State: forgereplay.StateNew}}
	h.WithForgeReplayStore(replay)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))

	requestBody := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","operation":"repo.read"}`
	rec := httptest.NewRecorder()
	h.AuthorizeForgeWorkload(rec, signedForgeRequest(t, requestBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if replay.completedID != "req-0000000000000001" || replay.completedCode != http.StatusOK {
		t.Fatalf("completion = (%q, %d)", replay.completedID, replay.completedCode)
	}
	if len(replay.completedBody) == 0 {
		t.Fatal("expected cached response body")
	}
}

func TestAuthorizeForgeWorkloadFailsClosedWhenDecisionCannotBePersisted(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()
	replay := &fakeForgeReplayStore{
		result:      forgereplay.Result{State: forgereplay.StateNew},
		completeErr: errors.New("storage unavailable"),
	}
	h.WithForgeReplayStore(replay)

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows("implementer"))

	requestBody := `{"request_id":"req-0000000000000001","task_id":"task-1","run_id":"run-1","operation":"repo.read"}`
	rec := httptest.NewRecorder()
	h.AuthorizeForgeWorkload(rec, signedForgeRequest(t, requestBody))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
