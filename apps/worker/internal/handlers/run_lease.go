package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// ErrRunLeaseUnavailable means a run is already owned, not queued, or its
// previous lease expired. Lease expiry never authorizes automatic replay.
var ErrRunLeaseUnavailable = errors.New("run execution lease unavailable")

// RunLeaseManager implements opt-in run liveness. The sweep only persists a
// review marker; it never changes run status or re-executes side effects.
// The database must have migration 025 installed before the feature is enabled.
type RunLeaseManager struct {
	db                *sql.DB
	logger            *slog.Logger
	ttl               time.Duration
	heartbeatInterval time.Duration
}

func NewRunLeaseManager(db *sql.DB, logger *slog.Logger) *RunLeaseManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &RunLeaseManager{db: db, logger: logger, ttl: 90 * time.Second, heartbeatInterval: 20 * time.Second}
}

func (m *RunLeaseManager) CheckSchema(ctx context.Context) error {
	if m == nil || m.db == nil {
		return errors.New("run lease database unavailable")
	}
	rows, err := m.db.QueryContext(ctx, `SELECT execution_lease_token, execution_lease_expires_at, execution_heartbeat_at, recovery_review_required_at FROM agent_runs LIMIT 0`)
	if err != nil {
		return fmt.Errorf("run lease schema not installed (migration 025 required): %w", err)
	}
	return rows.Close()
}

// Acquire is a single atomic, status-fenced ownership claim. Expired leases are
// not stealable: their prior owner may still be executing non-idempotent work.
func (m *RunLeaseManager) Acquire(ctx context.Context, runID, token string, now time.Time, ttl time.Duration) error {
	if m == nil || m.db == nil || runID == "" || token == "" || ttl <= 0 {
		return errors.New("invalid run lease claim")
	}
	result, err := m.db.ExecContext(ctx, `
        UPDATE agent_runs
        SET execution_lease_token=$1, execution_lease_expires_at=$2,
            execution_heartbeat_at=$3, recovery_review_required_at=NULL
        WHERE id=$4 AND status IN ('queued','admitting')
          AND execution_lease_token IS NULL
    `, token, now.Add(ttl), now, runID)
	if err != nil {
		return fmt.Errorf("acquire run execution lease: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("confirm execution lease: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("%w: %s", ErrRunLeaseUnavailable, runID)
	}
	return nil
}

// Renew fences both ownership and expiration. An expired token cannot revive
// itself. A running executor must stop if renewal fails or loses ownership.
func (m *RunLeaseManager) Renew(ctx context.Context, runID, token string, now time.Time, ttl time.Duration) error {
	if m == nil || m.db == nil || runID == "" || token == "" || ttl <= 0 {
		return errors.New("invalid run lease renewal")
	}
	result, err := m.db.ExecContext(ctx, `
        UPDATE agent_runs
        SET execution_heartbeat_at=$1, execution_lease_expires_at=$2
        WHERE id=$3 AND execution_lease_token=$4
          AND status IN ('admitting','running')
          AND execution_lease_expires_at > $1
          AND recovery_review_required_at IS NULL
    `, now, now.Add(ttl), runID, token)
	if err != nil {
		return fmt.Errorf("renew run execution lease: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("confirm run lease renewal: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("%w: %s", ErrRunLeaseUnavailable, runID)
	}
	return nil
}

// Release intentionally retains the lease when a run is still running. That
// stale marker is essential evidence for recovery after persistence failures.
func (m *RunLeaseManager) Release(ctx context.Context, runID, token string) error {
	if m == nil || m.db == nil || runID == "" || token == "" {
		return errors.New("invalid run lease release")
	}
	_, err := m.db.ExecContext(ctx, `
        UPDATE agent_runs
        SET execution_lease_token=NULL, execution_lease_expires_at=NULL,
            execution_heartbeat_at=NULL, recovery_review_required_at=NULL
        WHERE id=$1 AND execution_lease_token=$2
          AND status IN ('completed','failed','cancelled','paused')
    `, runID, token)
	if err != nil {
		return fmt.Errorf("release run lease: %w", err)
	}
	return nil
}

// FlagExpired is an idempotent, bounded, non-destructive reconciliation pass.
// Expiration means "needs review", *not* "the prior worker is dead".
// Only opted-in runs with a non-null token are considered.
func (m *RunLeaseManager) FlagExpired(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if m == nil || m.db == nil {
		return nil, errors.New("run lease database unavailable")
	}
	if limit <= 0 || limit > 100 {
		return nil, errors.New("invalid expired-run sweep limit")
	}
	rows, err := m.db.QueryContext(ctx, `
        UPDATE agent_runs
        SET recovery_review_required_at=$1
        WHERE id IN (
            SELECT id FROM agent_runs
            WHERE status IN ('running','admitting')
              AND execution_lease_token IS NOT NULL
              AND execution_lease_expires_at <= $1
              AND recovery_review_required_at IS NULL
            ORDER BY execution_lease_expires_at, id LIMIT $2
        )
          AND status IN ('running','admitting')
          AND execution_lease_expires_at <= $1
          AND recovery_review_required_at IS NULL
        RETURNING id
    `, now, limit)
	if err != nil {
		return nil, fmt.Errorf("flag expired runs for review: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan expired run: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read expired runs: %w", err)
	}
	return ids, nil
}

// Execute holds ownership while the supplied worker runs. The same context is
// cancelled if any renewal fails. No retry or reassignment is initiated.
func (m *RunLeaseManager) Execute(ctx context.Context, runID string, execute func(context.Context) error) error {
	if execute == nil {
		return errors.New("run executor not configured")
	}
	token := uuid.NewString()
	if err := m.Acquire(ctx, runID, token, time.Now().UTC(), m.ttl); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(m.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				done <- nil
				return
			case <-ticker.C:
				renewCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				err := m.Renew(renewCtx, runID, token, time.Now().UTC(), m.ttl)
				stop()
				if err != nil {
					cancel()
					done <- err
					return
				}
			}
		}
	}()
	executionErr := execute(runCtx)
	// Never acknowledge work that outlived its cancellation boundary.
	if executionErr == nil && runCtx.Err() != nil {
		executionErr = runCtx.Err()
	}
	cancel()
	renewErr := <-done
	cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	releaseErr := m.Release(cleanupCtx, runID, token)
	stop()
	return errors.Join(executionErr, renewErr, releaseErr)
}

// RunReviewSweep runs only when the feature is explicitly enabled. It flags
// candidates for human/operator reconciliation, never changes their status.
func (m *RunLeaseManager) RunReviewSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			ids, err := m.FlagExpired(scanCtx, time.Now().UTC(), 50)
			cancel()
			if err != nil {
				m.logger.Error("run liveness reconciliation failed", "error", err)
				continue
			}
			for _, id := range ids {
				m.logger.Warn("run lease expired; manual review required; no automatic replay", "run_id", id)
			}
		}
	}
}
