package db

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"os"
	"testing"
	"time"
)

func newBrowserJobTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := New("file:" + t.TempDir() + "/jobs.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = database.RunMigrations("migrations"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"org-a", "org-b"} {
		if _, err = database.Exec(`INSERT INTO organizations(id,name,slug) VALUES(?,?,?)`, id, id, id); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestBrowserJobsTenantIdempotencyAndConflict(t *testing.T) {
	db := newBrowserJobTestDB(t)
	repo := NewBrowserJobStore(db.DB)
	ctx := context.Background()
	job, created, err := repo.SubmitBrowserJob(ctx, "org-a", "idem-1", []byte(`{"start_url":"https://example.org/"}`))
	if err != nil || !created {
		t.Fatalf("submit: %v %v", created, err)
	}
	same, created, err := repo.SubmitBrowserJob(ctx, "org-a", "idem-1", []byte(`{"start_url":"https://example.org/"}`))
	if err != nil || created || same.ID != job.ID {
		t.Fatalf("duplicate: %+v %v %v", same, created, err)
	}
	if _, _, err = repo.SubmitBrowserJob(ctx, "org-a", "idem-1", []byte(`{"start_url":"https://other.org/"}`)); !errors.Is(err, ErrBrowserJobConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if _, err = repo.GetBrowserJob(ctx, "org-b", job.ID); !errors.Is(err, ErrBrowserJobNotFound) {
		t.Fatalf("cross tenant Get: %v", err)
	}
	other, created, err := repo.SubmitBrowserJob(ctx, "org-b", "idem-1", []byte(`{"start_url":"https://example.org/"}`))
	if err != nil || !created || other.ID == job.ID {
		t.Fatalf("other tenant submit: %v %v", created, err)
	}
}

func TestBrowserJobClaimFencingAndLeaseExpiry(t *testing.T) {
	db := newBrowserJobTestDB(t)
	repo := NewBrowserJobStore(db.DB)
	ctx := context.Background()
	job, _, err := repo.SubmitBrowserJob(ctx, "org-a", "idem-2", []byte(`{"read_only":true}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	epoch, err := repo.ClaimBrowserJob(ctx, job.ID, "worker-a", now, 2*time.Minute)
	if err != nil || epoch != 1 {
		t.Fatalf("claim: %d %v", epoch, err)
	}
	if _, err = repo.ClaimBrowserJob(ctx, job.ID, "worker-b", now, 2*time.Minute); !errors.Is(err, ErrBrowserJobNotClaimable) {
		t.Fatalf("duplicate claim: %v", err)
	}
	if err = repo.CompleteBrowserJob(ctx, job.ID, "worker-b", epoch, now.Add(time.Minute), "passed", []byte(`{"status":"passed"}`)); !errors.Is(err, ErrBrowserJobStaleClaim) {
		t.Fatalf("wrong worker completion: %v", err)
	}
	if err = repo.CompleteBrowserJob(ctx, job.ID, "worker-a", epoch+1, now.Add(time.Minute), "passed", []byte(`{"status":"passed"}`)); !errors.Is(err, ErrBrowserJobStaleClaim) {
		t.Fatalf("wrong epoch completion: %v", err)
	}
	if err = repo.CompleteBrowserJob(ctx, job.ID, "worker-a", epoch, now.Add(time.Minute), "passed", []byte(`{"status":"passed"}`)); err != nil {
		t.Fatal(err)
	}
	done, err := repo.GetBrowserJob(ctx, "org-a", job.ID)
	if err != nil || done.State != "passed" || string(done.Result) != `{"status":"passed"}` {
		t.Fatalf("completed: %+v %v", done, err)
	}
	if err = repo.CompleteBrowserJob(ctx, job.ID, "worker-a", epoch, now.Add(time.Minute), "passed", []byte(`{"status":"passed"}`)); !errors.Is(err, ErrBrowserJobStaleClaim) {
		t.Fatalf("replay completion: %v", err)
	}

	risky, _, err := repo.SubmitBrowserJob(ctx, "org-a", "idem-3", []byte(`{"read_only":true}`))
	if err != nil {
		t.Fatal(err)
	}
	staleEpoch, err := repo.ClaimBrowserJob(ctx, risky.ID, "worker-a", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	count, err := repo.ReconcileExpiredBrowserJobs(ctx, now.Add(2*time.Minute))
	if err != nil || count != 1 {
		t.Fatalf("reconcile: %d %v", count, err)
	}
	uncertain, err := repo.GetBrowserJob(ctx, "org-a", risky.ID)
	if err != nil || uncertain.State != "needs_review" {
		t.Fatalf("uncertain state: %+v %v", uncertain, err)
	}
	if _, err = repo.ClaimBrowserJob(ctx, risky.ID, "worker-b", now.Add(2*time.Minute), time.Minute); !errors.Is(err, ErrBrowserJobNotClaimable) {
		t.Fatalf("auto replay allowed: %v", err)
	}
	if err = repo.CompleteBrowserJob(ctx, risky.ID, "worker-a", staleEpoch, now.Add(2*time.Minute), "passed", []byte(`{}`)); !errors.Is(err, ErrBrowserJobStaleClaim) {
		t.Fatalf("stale completion accepted: %v", err)
	}
}

func TestBrowserJobInvalidInputsFailClosed(t *testing.T) {
	db := newBrowserJobTestDB(t)
	repo := NewBrowserJobStore(db.DB)
	ctx := context.Background()
	for _, key := range []string{"", "../traversal"} {
		if _, _, err := repo.SubmitBrowserJob(ctx, "org-a", key, []byte(`{}`)); err == nil {
			t.Errorf("accepted key %q", key)
		}
	}
	if _, _, err := repo.SubmitBrowserJob(ctx, "org-a", "empty", nil); err == nil {
		t.Error("accepted empty spec")
	}
}

func TestBrowserJobLeaseRenewalRequiresCurrentLiveClaim(t *testing.T) {
	database := newBrowserJobTestDB(t)
	repo := NewBrowserJobStore(database.DB)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	job, _, err := repo.SubmitBrowserJob(ctx, "org-a", "idem-renew", []byte(`{"read_only":true}`))
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := repo.ClaimBrowserJob(ctx, job.ID, "worker-a", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.RenewBrowserJobLease(ctx, job.ID, "worker-a", epoch, now.Add(30*time.Second), 2*time.Minute); err != nil {
		t.Fatalf("renew valid: %v", err)
	}
	if err = repo.RenewBrowserJobLease(ctx, job.ID, "worker-b", epoch, now.Add(time.Minute), time.Minute); !errors.Is(err, ErrBrowserJobStaleClaim) {
		t.Fatalf("wrong worker: %v", err)
	}
	if err = repo.RenewBrowserJobLease(ctx, job.ID, "worker-a", epoch+1, now.Add(time.Minute), time.Minute); !errors.Is(err, ErrBrowserJobStaleClaim) {
		t.Fatalf("wrong epoch: %v", err)
	}
	if err = repo.RenewBrowserJobLease(ctx, job.ID, "worker-a", epoch, now.Add(4*time.Minute), time.Minute); !errors.Is(err, ErrBrowserJobStaleClaim) {
		t.Fatalf("expired lease: %v", err)
	}
}

// This integration test is opt-in and must run against a disposable database.
func TestBrowserJobsPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DATABASE_URL to an ephemeral PostgreSQL DB")
	}
	db, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.RunMigrations("migrations"); err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}
	org := uuid.NewString()
	ctx := context.Background()
	if _, err = db.ExecContext(ctx, `INSERT INTO organizations(id,name,slug) VALUES($1,$2,$3)`, org, "Browser test", org); err != nil {
		t.Fatal(err)
	}
	repo := NewBrowserJobStore(db.DB)
	job, created, err := repo.SubmitBrowserJob(ctx, org, "postgres-idem", []byte(`{"version":1,"start_url":"https://example.com"}`))
	if err != nil || !created {
		t.Fatalf("submit: %v %v", created, err)
	}
	same, created, err := repo.SubmitBrowserJob(ctx, org, "postgres-idem", []byte(`{"version":1,"start_url":"https://example.com"}`))
	if err != nil || created || same.ID != job.ID {
		t.Fatalf("duplicate: %v %v", created, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	epoch, err := repo.ClaimBrowserJob(ctx, job.ID, "worker-pg", now, time.Minute)
	if err != nil || epoch != 1 {
		t.Fatalf("claim: %d %v", epoch, err)
	}
	if err = repo.RenewBrowserJobLease(ctx, job.ID, "worker-pg", epoch, now.Add(20*time.Second), time.Minute); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err = repo.CompleteBrowserJob(ctx, job.ID, "worker-pg", epoch, now.Add(30*time.Second), "passed", []byte(`{"status":"passed"}`)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	got, err := repo.GetBrowserJob(ctx, org, job.ID)
	if err != nil || got.State != "passed" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err = repo.GetBrowserJob(ctx, uuid.NewString(), job.ID); !errors.Is(err, ErrBrowserJobNotFound) {
		t.Fatalf("cross tenant: %v", err)
	}
}

func TestBrowserJobsRejectInvalidJSONBeforePersistence(t *testing.T) {
	database := newBrowserJobTestDB(t)
	repo := NewBrowserJobStore(database.DB)
	ctx := context.Background()
	if _, _, err := repo.SubmitBrowserJob(ctx, "org-a", "invalid-json", []byte("not-json")); err == nil {
		t.Fatal("invalid spec stored")
	}
	job, _, err := repo.SubmitBrowserJob(ctx, "org-a", "valid-json", []byte(`{"version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	epoch, err := repo.ClaimBrowserJob(ctx, job.ID, "worker-json", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteBrowserJob(ctx, job.ID, "worker-json", epoch, now, "passed", []byte("not-json")); err == nil {
		t.Fatal("invalid result stored")
	}
}
