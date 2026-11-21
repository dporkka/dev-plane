package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// Run with an isolated, disposable PostgreSQL database to validate that the
// SQLite-proven lease SQL also works with production timestamp/RETURNING rules.
// This test creates a per-connection TEMP table and never modifies real data.
func TestRunLeasePostgresSQLConformance(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL not set; use isolated PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
        CREATE TEMP TABLE agent_runs (
            id TEXT PRIMARY KEY, status TEXT NOT NULL,
            execution_lease_token TEXT,
            execution_lease_expires_at TIMESTAMPTZ,
            execution_heartbeat_at TIMESTAMPTZ,
            recovery_review_required_at TIMESTAMPTZ
        );
        INSERT INTO agent_runs(id,status) VALUES ('pg-run','queued');
    `)
	if err != nil {
		t.Fatal(err)
	}
	lease := NewRunLeaseManager(db, slog.Default())
	ctx := context.Background()
	if err := lease.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	if err := lease.Acquire(ctx, "pg-run", "owner-one", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := lease.Acquire(ctx, "pg-run", "owner-two", now, time.Minute); !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("duplicate PG lease acquired: %v", err)
	}
	if _, err := db.Exec(`UPDATE agent_runs SET status='running' WHERE id='pg-run'`); err != nil {
		t.Fatal(err)
	}
	if err := lease.Renew(ctx, "pg-run", "owner-one", now.Add(20*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := lease.Renew(ctx, "pg-run", "owner-two", now.Add(21*time.Second), time.Minute); !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("foreign PG renewal: %v", err)
	}
	flagged, err := lease.FlagExpired(ctx, now.Add(90*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(flagged) != 1 || flagged[0] != "pg-run" {
		t.Fatalf("PG review marker mismatch: %v", flagged)
	}
	flagged, err = lease.FlagExpired(ctx, now.Add(90*time.Second), 10)
	if err != nil || len(flagged) != 0 {
		t.Fatalf("PG review marker not idempotent: %v %v", flagged, err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id='pg-run'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("PG advisory sweep changed run state to %q", status)
	}
	if _, err := db.Exec(`UPDATE agent_runs SET status='failed' WHERE id='pg-run'`); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(ctx, "pg-run", "owner-one"); err != nil {
		t.Fatal(err)
	}
	var tok sql.NullString
	if err := db.QueryRow(`SELECT execution_lease_token FROM agent_runs WHERE id='pg-run'`).Scan(&tok); err != nil {
		t.Fatal(err)
	}
	if tok.Valid {
		t.Fatalf("PG failed terminal lease not released: %s", tok.String)
	}
}
