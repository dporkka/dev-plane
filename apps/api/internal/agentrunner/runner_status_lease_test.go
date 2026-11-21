package agentrunner

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/execution"
	"github.com/ai-dev-control-plane/models"
)

func TestRunStatusWritesRequireCurrentLease(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) { testRunStatusWrites(t, "sqlite3", ":memory:") })
	t.Run("PostgreSQL", func(t *testing.T) {
		dsn := os.Getenv("POSTGRES_TEST_DATABASE_URL")
		if dsn == "" {
			t.Skip("POSTGRES_TEST_DATABASE_URL not set; use isolated PostgreSQL")
		}
		testRunStatusWrites(t, "postgres", dsn)
	})
}

func testRunStatusWrites(t *testing.T, driver, dsn string) {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TEMP TABLE agent_runs (
		id TEXT PRIMARY KEY, status TEXT NOT NULL, execution_lease_token TEXT,
		execution_lease_expires_at TIMESTAMPTZ, recovery_review_required_at TIMESTAMPTZ,
		started_at TIMESTAMPTZ, completed_at TIMESTAMPTZ, updated_at TIMESTAMPTZ,
		total_cost DECIMAL(10,6), summary TEXT, error_message TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	r := &Runner{db: db, logger: slog.Default()}
	now := time.Now().UTC()
	base := context.Background()
	owner := execution.WithRunStatusLease(base, "run-1", "owner")
	preflightOnly := execution.WithEffectGuard(base, func(context.Context) error { return nil })
	for _, tc := range []struct {
		name, status, token string
		expires             time.Time
		recovery            *time.Time
		ctx                 context.Context
		allowed             bool
	}{
		{"admitting", "admitting", "owner", now.Add(time.Hour), nil, owner, true},
		{"running", "running", "owner", now.Add(time.Hour), nil, owner, true},
		{"token replaced", "running", "next-owner", now.Add(time.Hour), nil, owner, false},
		{"expired", "running", "owner", now.Add(-time.Second), nil, owner, false},
		{"cancelled", "cancelled", "owner", now.Add(time.Hour), nil, owner, false},
		{"recovery required", "running", "owner", now.Add(time.Hour), &now, owner, false},
		{"successful preflight alone", "running", "owner", now.Add(time.Hour), nil, preflightOnly, false},
	} {
		for _, update := range []struct {
			status string
			write  func(context.Context) error
		}{
			{models.AgentRunStatusRunning, func(ctx context.Context) error {
				return r.updateRunStatus(ctx, "run-1", models.AgentRunStatusRunning, nil)
			}},
			{models.AgentRunStatusFailed, func(ctx context.Context) error { return r.failRun(ctx, "run-1", "test failure") }},
			{models.AgentRunStatusPaused, func(ctx context.Context) error { return r.pauseRun(ctx, "run-1", "test pause") }},
			{models.AgentRunStatusCompleted, func(ctx context.Context) error {
				return r.updateRunCompletion(ctx, "run-1", models.AgentRunStatusCompleted, "test completion", &RunState{})
			}},
		} {
			t.Run(tc.name+"/"+update.status, func(t *testing.T) {
				if _, err := db.Exec(`DELETE FROM agent_runs`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO agent_runs (
					id, status, execution_lease_token, execution_lease_expires_at, recovery_review_required_at
				) VALUES ('run-1', $1, $2, $3, $4)`, tc.status, tc.token, tc.expires, tc.recovery); err != nil {
					t.Fatal(err)
				}
				err := update.write(tc.ctx)
				expected := tc.status
				if tc.allowed {
					expected = update.status
					if update.status != models.AgentRunStatusFailed && err != nil {
						t.Fatalf("valid lease rejected: %v", err)
					}
				} else if !errors.Is(err, execution.ErrRunLeaseUnavailable) {
					t.Fatalf("want ownership rejection, got %v", err)
				}
				var status string
				var updated bool
				if err := db.QueryRow(`SELECT status, updated_at IS NOT NULL FROM agent_runs WHERE id = 'run-1'`).Scan(&status, &updated); err != nil {
					t.Fatal(err)
				}
				if status != expected || updated != tc.allowed {
					t.Fatalf("status = %q, updated = %t; want %q, updated = %t", status, updated, expected, tc.allowed)
				}
			})
		}
	}
}
