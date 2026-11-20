package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/models"
	"github.com/nats-io/nats.go"

	_ "github.com/mattn/go-sqlite3"
)

func newRunLeaseTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
        CREATE TABLE agent_runs (
            id TEXT PRIMARY KEY, status TEXT NOT NULL,
            execution_lease_token TEXT,
            execution_lease_expires_at TIMESTAMP,
            execution_heartbeat_at TIMESTAMP,
            recovery_review_required_at TIMESTAMP
        );
        INSERT INTO agent_runs (id,status) VALUES
            ('queued', 'queued'), ('live','running'),
            ('completed','completed'), ('canceled','cancelled');
    `)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRunLeaseClaimIsExclusiveAndCannotStealActiveRun(t *testing.T) {
	db := newRunLeaseTestDB(t)
	lease := NewRunLeaseManager(db, slog.Default())
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	if err := lease.Acquire(context.Background(), "queued", "owner-a", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := lease.Acquire(context.Background(), "queued", "owner-b", now, time.Minute); !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("claim collision: %v", err)
	}
	if err := lease.Acquire(context.Background(), "live", "owner-c", now, time.Minute); !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("active run claim: %v", err)
	}
	if err := lease.Acquire(context.Background(), "completed", "owner-c", now, time.Minute); !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("terminal run claim: %v", err)
	}
}

func TestRunLeaseRenewFencesExpiredOrWrongToken(t *testing.T) {
	db := newRunLeaseTestDB(t)
	lease := NewRunLeaseManager(db, slog.Default())
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	if err := lease.Acquire(context.Background(), "queued", "owner-a", now, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec("UPDATE agent_runs SET status = 'running' WHERE id = 'queued'")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Renew(context.Background(), "queued", "owner-b", now.Add(time.Second), time.Minute); !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("wrong owner renewed: %v", err)
	}
	if err := lease.Renew(context.Background(), "queued", "owner-a", now.Add(30*time.Second), time.Minute); err != nil {
		t.Fatalf("valid renew: %v", err)
	}
	if err := lease.Renew(context.Background(), "queued", "owner-a", now.Add(92*time.Second), time.Minute); !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("expired owner renewed: %v", err)
	}
}

func TestRunLeaseReleaseKeepsNonterminalRunMarkedForRecovery(t *testing.T) {
	db := newRunLeaseTestDB(t)
	lease := NewRunLeaseManager(db, slog.Default())
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	if err := lease.Acquire(context.Background(), "queued", "owner-a", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(context.Background(), "queued", "owner-a"); err != nil {
		t.Fatal(err)
	}
	var token string
	if err := db.QueryRow("SELECT execution_lease_token FROM agent_runs WHERE id='queued'").Scan(&token); err != nil {
		t.Fatal(err)
	}
	if token != "owner-a" {
		t.Fatalf("nonterminal lease removed: %q", token)
	}
	if _, err := db.Exec("UPDATE agent_runs SET status = 'failed' WHERE id='queued'"); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(context.Background(), "queued", "owner-b"); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(context.Background(), "queued", "owner-a"); err != nil {
		t.Fatal(err)
	}
	var after sql.NullString
	if err := db.QueryRow("SELECT execution_lease_token FROM agent_runs WHERE id='queued'").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after.Valid {
		t.Fatalf("terminal lease not released: %s", after.String)
	}
}

func TestRunLeaseSweepMarksOnlyExpiredLiveRunsWithoutReplaying(t *testing.T) {
	db := newRunLeaseTestDB(t)
	lease := NewRunLeaseManager(db, slog.Default())
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		id, status, token string
		expire            time.Time
	}{
		{"live", "running", "expired", now.Add(-time.Second)},
		{"completed", "completed", "old", now.Add(-time.Second)},
		{"canceled", models.AgentRunStatusCancelled, "old", now.Add(-time.Second)},
		{"queued", "queued", "active", now.Add(time.Minute)},
	} {
		if _, err := db.Exec("UPDATE agent_runs SET execution_lease_token=?,execution_lease_expires_at=? WHERE id=?", row.token, row.expire, row.id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := lease.FlagExpired(context.Background(), now, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "live" {
		t.Fatalf("flagged=%v, want only live", got)
	}
	got, err = lease.FlagExpired(context.Background(), now.Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("duplicate/revived flags: %v", got)
	}
	// An admitted worker can die before changing status to running.
	if _, err := db.Exec("INSERT INTO agent_runs (id,status,execution_lease_token,execution_lease_expires_at) VALUES ('admitting','admitting','owner-z',?)", now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err = lease.FlagExpired(context.Background(), now, 20)
	if err != nil || len(got) != 1 || got[0] != "admitting" {
		t.Fatalf("admitting claim not flagged for review: %v %v", got, err)
	}
	var status string
	var review sql.NullTime
	if err := db.QueryRow("SELECT status,recovery_review_required_at FROM agent_runs WHERE id='live'").Scan(&status, &review); err != nil {
		t.Fatal(err)
	}
	if status != "running" || !review.Valid {
		t.Fatalf("reconciler changed execution state or failed to mark review: %q %v", status, review)
	}
}

// A running executor receives cancellation when another token appears. The
// liveness wrapper must never hide the fencing failure as successful execution.
func TestRunLeaseExecuteCancelsOnLostOwnership(t *testing.T) {
	db := newRunLeaseTestDB(t)
	lease := NewRunLeaseManager(db, slog.Default())
	lease.heartbeatInterval = 5 * time.Millisecond
	lease.ttl = 100 * time.Millisecond
	sawCancel := false
	err := lease.Execute(context.Background(), "queued", func(ctx context.Context) error {
		if _, err := db.Exec("UPDATE agent_runs SET status='running', execution_lease_token='other' WHERE id='queued'"); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			sawCancel = true
			return ctx.Err()
		case <-time.After(time.Second):
			return errors.New("lease failure did not cancel executor")
		}
	})
	if !sawCancel || !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("lost lease: canceled=%v error=%v", sawCancel, err)
	}
}

type leaseCompletingExecutor struct {
	db    *sql.DB
	calls int
}

func (e *leaseCompletingExecutor) ExecuteRun(ctx context.Context, runID string) error {
	e.calls++
	_, err := e.db.ExecContext(ctx, "UPDATE agent_runs SET status='completed' WHERE id=?", runID)
	return err
}

func TestHandleRunTriggeredUsesLeaseAndReleasesAfterConfirmedCompletion(t *testing.T) {
	db := newRunLeaseTestDB(t)
	executor := &leaseCompletingExecutor{db: db}
	handler := NewRunHandler(db, slog.Default(), nil).
		WithRunExecutor(executor).
		WithRunLease(NewRunLeaseManager(db, slog.Default()))
	if err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"queued","task_id":"task-1"}`)}); err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls=%d", executor.calls)
	}
	var status string
	var token sql.NullString
	if err := db.QueryRow("SELECT status, execution_lease_token FROM agent_runs WHERE id='queued'").Scan(&status, &token); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || token.Valid {
		t.Fatalf("status=%q token=%v; expected completed with released lease", status, token)
	}
	// Duplicate delivery must never run the action again.
	if err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"queued","task_id":"task-1"}`)}); !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("duplicate replay not denied: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("duplicate execution detected: calls=%d", executor.calls)
	}
}

func TestRunLeaseExecuteDoesNotAcknowledgeCanceledExecutor(t *testing.T) {
	db := newRunLeaseTestDB(t)
	lease := NewRunLeaseManager(db, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	err := lease.Execute(ctx, "queued", func(context.Context) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled work was accepted as success: %v", err)
	}
}

type countingAdmission struct{ releases int }

func (a *countingAdmission) AdmitRun(context.Context, string, string) (RunAdmissionDecision, error) {
	return RunAdmissionDecision{Allowed: true}, nil
}
func (a *countingAdmission) ReleaseRun(context.Context, string) error { a.releases++; return nil }
func TestRunLeaseRejectionDoesNotReleaseAnotherWorkersAdmission(t *testing.T) {
	db := newRunLeaseTestDB(t)
	if _, err := db.Exec("UPDATE agent_runs SET execution_lease_token='foreign' WHERE id='queued'"); err != nil {
		t.Fatal(err)
	}
	executor := &leaseCompletingExecutor{db: db}
	admission := &countingAdmission{}
	handler := NewRunHandler(db, slog.Default(), nil).
		WithRunExecutor(executor).
		WithRunAdmission(admission).
		WithRunLease(NewRunLeaseManager(db, slog.Default()))
	err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"queued","task_id":"task-1"}`)})
	if !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("foreign lease not rejected: %v", err)
	}
	if executor.calls != 0 || admission.releases != 0 {
		t.Fatalf("foreign lease modified: calls=%d releases=%d", executor.calls, admission.releases)
	}
}
