package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	ErrBrowserJobConflict     = errors.New("browser job idempotency conflict")
	ErrBrowserJobNotFound     = errors.New("browser job not found")
	ErrBrowserJobNotClaimable = errors.New("browser job is not queued")
	ErrBrowserJobStaleClaim   = errors.New("browser job claim is stale or expired")
	browserKeyPattern         = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
)

// BrowserJobStore accepts a standard *sql.DB from API or worker. PostgreSQL-style
// $N parameters are also supported by the local SQLite driver.
type BrowserJobStore struct{ *sql.DB }

func NewBrowserJobStore(database *sql.DB) *BrowserJobStore { return &BrowserJobStore{DB: database} }

type BrowserJob struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	IdempotencyKey string `json:"idempotency_key"`
	SpecDigest     string `json:"spec_digest"`
	State          string `json:"state"`
	Payload        []byte `json:"payload"`
	Result         []byte `json:"result,omitempty"`
	ClaimEpoch     int64  `json:"-"`
}

// SubmitBrowserJob atomically deduplicates (organization, idempotency_key).
// The payload is an immutable, already validated v1 verification contract.
func (db *BrowserJobStore) SubmitBrowserJob(ctx context.Context, org, key string, payload []byte) (BrowserJob, bool, error) {
	if org == "" || !browserKeyPattern.MatchString(key) || len(payload) == 0 || len(payload) > 16384 || !json.Valid(payload) {
		return BrowserJob{}, false, errors.New("invalid browser job organization, idempotency key, or payload")
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	id := uuid.NewString()
	query := `INSERT INTO browser_jobs(id,organization_id,idempotency_key,spec_digest,payload,state) VALUES($1,$2,$3,$4,$5,'queued') ON CONFLICT (organization_id,idempotency_key) DO NOTHING`

	outcome, err := db.ExecContext(ctx, query, id, org, key, hash, string(payload))
	if err != nil {
		return BrowserJob{}, false, fmt.Errorf("submit browser job: %w", err)
	}
	affected, err := outcome.RowsAffected()
	if err != nil {
		return BrowserJob{}, false, err
	}
	readQuery := `SELECT id,organization_id,idempotency_key,spec_digest,payload,state,result,claim_epoch FROM browser_jobs WHERE organization_id=$1 AND idempotency_key=$2`

	job, err := scanBrowserJob(db.QueryRowContext(ctx, readQuery, org, key))
	if err != nil {
		return BrowserJob{}, false, fmt.Errorf("read browser job: %w", err)
	}
	if job.SpecDigest != hash {
		return BrowserJob{}, false, ErrBrowserJobConflict
	}
	return job, affected == 1, nil
}

func scanBrowserJob(row *sql.Row) (BrowserJob, error) {
	var job BrowserJob
	var result []byte
	err := row.Scan(&job.ID, &job.OrganizationID, &job.IdempotencyKey, &job.SpecDigest, &job.Payload, &job.State, &result, &job.ClaimEpoch)
	if err != nil {
		return BrowserJob{}, err
	}
	job.Result = result
	return job, nil
}

func (db *BrowserJobStore) GetBrowserJob(ctx context.Context, org, id string) (BrowserJob, error) {
	if org == "" || id == "" {
		return BrowserJob{}, ErrBrowserJobNotFound
	}
	query := `SELECT id,organization_id,idempotency_key,spec_digest,payload,state,result,claim_epoch FROM browser_jobs WHERE organization_id=$1 AND id=$2`

	job, err := scanBrowserJob(db.QueryRowContext(ctx, query, org, id))
	if errors.Is(err, sql.ErrNoRows) {
		return BrowserJob{}, ErrBrowserJobNotFound
	}
	return job, err
}

// ClaimBrowserJob is a CAS: only queued jobs can transition to running.
// Leases are never eligible for automatic re-claim after expiry.
func (db *BrowserJobStore) ClaimBrowserJob(ctx context.Context, id, worker string, now time.Time, ttl time.Duration) (int64, error) {
	if id == "" || !browserKeyPattern.MatchString(worker) || ttl < time.Second || ttl > 10*time.Minute {
		return 0, errors.New("invalid browser job claim")
	}
	query := `UPDATE browser_jobs SET state='running',claimed_by=$1,claim_epoch=claim_epoch+1,lease_until=$2,updated_at=CURRENT_TIMESTAMP WHERE id=$3 AND state='queued' RETURNING claim_epoch`

	var epoch int64
	err := db.QueryRowContext(ctx, query, worker, now.UTC().Add(ttl), id).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrBrowserJobNotClaimable
	}
	return epoch, err
}

// CompleteBrowserJob is fenced by claimant, epoch, and unexpired lease.
// Callers must separately authenticate and authorize internal worker identities.
func (db *BrowserJobStore) CompleteBrowserJob(ctx context.Context, id, worker string, epoch int64, now time.Time, state string, result []byte) error {
	if state != "passed" && state != "failed" {
		return errors.New("invalid browser job terminal state")
	}
	if len(result) == 0 || len(result) > 16384 || !json.Valid(result) || !browserKeyPattern.MatchString(worker) || epoch < 1 {
		return errors.New("invalid browser job completion")
	}
	query := `UPDATE browser_jobs SET state=$1,result=$2,claimed_by=NULL,lease_until=NULL,updated_at=CURRENT_TIMESTAMP WHERE id=$3 AND state='running' AND claimed_by=$4 AND claim_epoch=$5 AND lease_until>$6`

	outcome, err := db.ExecContext(ctx, query, state, string(result), id, worker, epoch, now.UTC())
	if err != nil {
		return err
	}
	affected, err := outcome.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrBrowserJobStaleClaim
	}
	return nil
}

// ReconcileExpiredBrowserJobs never requeues an expired execution. External
// actions could already have occurred; operator review is required instead.
func (db *BrowserJobStore) ReconcileExpiredBrowserJobs(ctx context.Context, now time.Time) (int64, error) {
	query := `UPDATE browser_jobs SET state='needs_review',claimed_by=NULL,lease_until=NULL,updated_at=CURRENT_TIMESTAMP WHERE state='running' AND lease_until<=$1`

	outcome, err := db.ExecContext(ctx, query, now.UTC())
	if err != nil {
		return 0, err
	}
	return outcome.RowsAffected()
}

// RenewBrowserJobLease extends a live claim without changing its epoch.
// A stale worker, expired lease, or reconciled job cannot renew.
func (db *BrowserJobStore) RenewBrowserJobLease(ctx context.Context, id, worker string, epoch int64, now time.Time, ttl time.Duration) error {
	if id == "" || !browserKeyPattern.MatchString(worker) || epoch < 1 || ttl < time.Second || ttl > 10*time.Minute {
		return errors.New("invalid browser job renewal")
	}
	query := `UPDATE browser_jobs SET lease_until=$1,updated_at=CURRENT_TIMESTAMP WHERE id=$2 AND claimed_by=$3 AND claim_epoch=$4 AND state='running' AND lease_until>$5`
	outcome, err := db.ExecContext(ctx, query, now.UTC().Add(ttl), id, worker, epoch, now.UTC())
	if err != nil {
		return err
	}
	affected, err := outcome.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrBrowserJobStaleClaim
	}
	return nil
}
