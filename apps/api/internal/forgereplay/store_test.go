package forgereplay

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func testStore(t *testing.T) (*Store, func()) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE forge_workload_requests (
			request_id TEXT PRIMARY KEY,
			workload_id TEXT NOT NULL,
			body_sha256 TEXT NOT NULL,
			task_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'pending',
			response_status INTEGER,
			response_body TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME
		)
	`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return NewStore(db), func() { _ = db.Close() }
}

func request(body string) Request {
	return Request{
		ID:         "req-0000000000000001",
		WorkloadID: "nulang-cloud",
		TaskID:     "task-1",
		RunID:      "run-1",
		Operation:  "repo.read",
		Body:       []byte(body),
	}
}

func TestClaimCreatesNewRequest(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	result, err := store.Claim(context.Background(), request(`{"request_id":"req-0000000000000001"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateNew {
		t.Fatalf("state = %q, want %q", result.State, StateNew)
	}
}

func TestClaimReplaysCompletedIdenticalRequest(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()
	ctx := context.Background()
	req := request(`{"request_id":"req-0000000000000001"}`)

	if _, err := store.Claim(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, req.ID, 200, []byte(`{"allowed":true}`)); err != nil {
		t.Fatal(err)
	}

	result, err := store.Claim(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateReplay {
		t.Fatalf("state = %q, want %q", result.State, StateReplay)
	}
	if result.ResponseStatus != 200 || string(result.ResponseBody) != `{"allowed":true}` {
		t.Fatalf("cached response = (%d, %q)", result.ResponseStatus, result.ResponseBody)
	}
}

func TestClaimRejectsRequestIDReuseWithDifferentSignedBody(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := store.Claim(ctx, request(`{"operation":"repo.read"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := store.Claim(ctx, request(`{"operation":"change.merge"}`))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestClaimReportsIdenticalPendingRequestAsInFlight(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()
	ctx := context.Background()
	req := request(`{"operation":"repo.read"}`)

	if _, err := store.Claim(ctx, req); err != nil {
		t.Fatal(err)
	}
	result, err := store.Claim(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateInFlight {
		t.Fatalf("state = %q, want %q", result.State, StateInFlight)
	}
}

func TestClaimReportsUncertainExecutionWithoutReexecuting(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()
	ctx := context.Background()
	req := request(`{"operation":"commit.write"}`)

	if _, err := store.Claim(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkUncertain(ctx, req.ID, []byte("provider outcome unknown")); err != nil {
		t.Fatal(err)
	}

	result, err := store.Claim(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateUncertain {
		t.Fatalf("state = %q, want %q", result.State, StateUncertain)
	}
	if string(result.ResponseBody) != "provider outcome unknown" {
		t.Fatalf("uncertain detail = %q", result.ResponseBody)
	}
}

func TestClaimRejectsWeakRequestID(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()
	req := request("{}")
	req.ID = "short"

	if _, err := store.Claim(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
}
