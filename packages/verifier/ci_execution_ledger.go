package verifier

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	rp "github.com/ai-dev-control-plane/repoprotocol"
)

var ErrCIExecutionLifecycle = errors.New("CI execution lifecycle missing, invalid, or already advanced")

// ControllerCILedger is the persistence boundary for a trusted CI controller.
// It is NOT a remote provider adapter or an agent API. Its writer must run
// outside job containers and observe the actual checkout, worker assignment,
// process start and exit. The signer can use a separate read-only connection.
// A caller-supplied exit code is authoritative ONLY in that trusted controller.
// No scheduler/runner integration or OS sandbox is implied by this type.
type ControllerCILedger struct {
	DB     *sql.DB
	Policy rp.RemoteCIPolicy
}

var _ AuthoritativeCIReader = ControllerCILedger{}

func (l ControllerCILedger) identity(q RemoteCIQuery) (rp.RemoteCIRecord, error) {
	p := l.Policy
	r := rp.RemoteCIRecord{Version: 1, Provider: p.Provider, Repository: p.Repository,
		HeadSHA: q.HeadSHA, PipelineID: q.PipelineID, DefinitionSHA256: p.DefinitionSHA256, RunID: q.RunID, JobID: q.JobID, AttemptID: q.AttemptID,
		State: rp.CIStateQueued, QueuedAt: time.Now().UTC()}
	if l.DB == nil || p.Provider != "dev-plane-ci" || q.PipelineID != p.PipelineID || q.RunID != p.RunID || q.JobID != p.JobID || q.AttemptID != p.AttemptID || r.Validate() != nil {
		return rp.RemoteCIRecord{}, ErrCIExecutionLifecycle
	}
	return r, nil
}

func identityArgs(r rp.RemoteCIRecord) []any {
	return []any{r.Provider, r.Repository, r.PipelineID, r.RunID, r.JobID, r.AttemptID, r.HeadSHA, r.DefinitionSHA256}
}

// Only fixed SQL fragments internal to this file are passed here, never
// operator- or job-controlled SQL. Numbered placeholders follow their first
// occurrence for compatibility with the SQLite driver.
func identityPredicate(offset int) string {
	return fmt.Sprintf("provider=$%d AND repository=$%d AND pipeline_id=$%d AND run_id=$%d AND job_id=$%d AND attempt_id=$%d AND head_sha=$%d AND definition_sha256=$%d",
		offset+1, offset+2, offset+3, offset+4, offset+5, offset+6, offset+7, offset+8)
}
func (l ControllerCILedger) one(ctx context.Context, query string, args ...any) error {
	result, err := l.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return ErrCIExecutionLifecycle
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrCIExecutionLifecycle
	}
	return nil
}

func (l ControllerCILedger) Enqueue(ctx context.Context, q RemoteCIQuery) error {
	r, err := l.identity(q)
	if err != nil {
		return err
	}
	return l.one(ctx, `INSERT INTO ci_execution_attempts
 (provider,repository,pipeline_id,run_id,job_id,attempt_id,head_sha,definition_sha256,state,queued_ms)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'queued',$9) ON CONFLICT DO NOTHING`, append(identityArgs(r), r.QueuedAt.UnixMilli())...)
}

func validControllerWorker(worker string) bool {
	return worker != "" && len(worker) <= 256 && worker == strings.TrimSpace(worker) && !strings.ContainsAny(worker, "\x00\r\n")
}
func leaseDigest(lease string) (string, error) {
	if len(lease) != 64 {
		return "", ErrCIExecutionLifecycle
	}
	raw, err := hex.DecodeString(lease)
	if err != nil {
		return "", ErrCIExecutionLifecycle
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// Assign reserves one worker and returns a random fencing token. Only the
// trusted controller may hold this token; never pass it into the CI process.
func (l ControllerCILedger) Assign(ctx context.Context, q RemoteCIQuery, worker string) (string, error) {
	r, err := l.identity(q)
	if err != nil || !validControllerWorker(worker) {
		return "", ErrCIExecutionLifecycle
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", ErrCIExecutionLifecycle
	}
	lease := hex.EncodeToString(raw)
	hash, err := leaseDigest(lease)
	if err != nil {
		return "", err
	}
	args := append([]any{worker, hash, time.Now().UTC().UnixMilli()}, identityArgs(r)...)
	err = l.one(ctx, `UPDATE ci_execution_attempts SET worker_id=$1,lease_hash=$2,assigned_ms=$3,state='assigned' WHERE `+
		identityPredicate(3)+` AND state='queued' AND queued_ms<=$3`, args...)
	if err != nil {
		return "", err
	}
	return lease, nil
}

func (l ControllerCILedger) Start(ctx context.Context, q RemoteCIQuery, worker, lease string) error {
	r, err := l.identity(q)
	hash, hashErr := leaseDigest(lease)
	if err != nil || hashErr != nil || !validControllerWorker(worker) {
		return ErrCIExecutionLifecycle
	}
	args := append([]any{time.Now().UTC().UnixMilli()}, identityArgs(r)...)
	args = append(args, worker, hash)
	return l.one(ctx, `UPDATE ci_execution_attempts SET started_ms=$1,state='running' WHERE `+
		identityPredicate(1)+` AND worker_id=$10 AND lease_hash=$11 AND state='assigned' AND assigned_ms<=$1`, args...)
}

// Finish records an observed process exit, not a job-reported success.
// Infrastructure failure, cancellation, or lost controller must remain
// non-release-eligible; they cannot be synthesized as completed exits.
func (l ControllerCILedger) Finish(ctx context.Context, q RemoteCIQuery, worker, lease string, exit int) error {
	r, err := l.identity(q)
	hash, hashErr := leaseDigest(lease)
	if err != nil || hashErr != nil || !validControllerWorker(worker) || exit < 0 || exit > 255 {
		return ErrCIExecutionLifecycle
	}
	args := append([]any{time.Now().UTC().UnixMilli(), exit}, identityArgs(r)...)
	args = append(args, worker, hash)
	return l.one(ctx, `UPDATE ci_execution_attempts SET finished_ms=$1,exit_code=$2,state='terminal',lease_hash=NULL WHERE `+
		identityPredicate(2)+` AND worker_id=$11 AND lease_hash=$12 AND state='running' AND started_ms<=$1`, args...)
}

func observedTime(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.UnixMilli(n.Int64).UTC()
}

// FetchAttempt implements the existing read-only attestor source contract.
// The policy is externally pinned to the current attempt, not selected from
// the ledger's most recent success.
func (l ControllerCILedger) FetchAttempt(ctx context.Context, q RemoteCIQuery) (rp.RemoteCIRecord, error) {
	r, err := l.identity(q)
	if err != nil {
		return rp.RemoteCIRecord{}, err
	}
	var queued, assigned, started, finished, exit sql.NullInt64
	var worker sql.NullString
	if err := l.DB.QueryRowContext(ctx, `SELECT state,worker_id,queued_ms,assigned_ms,started_ms,finished_ms,exit_code FROM ci_execution_attempts WHERE `+
		identityPredicate(0), identityArgs(r)...).Scan(&r.State, &worker, &queued, &assigned, &started, &finished, &exit); err != nil {
		return rp.RemoteCIRecord{}, ErrCIExecutionLifecycle
	}
	r.WorkerID = worker.String
	r.QueuedAt = observedTime(queued)
	r.AssignedAt = observedTime(assigned)
	r.StartedAt = observedTime(started)
	r.FinishedAt = observedTime(finished)
	if exit.Valid {
		code := int(exit.Int64)
		r.ExitCode = &code
		r.Outcome = rp.CIOutcomeTestsFailed
		if code == 0 {
			r.Outcome = rp.CIOutcomePassed
		}
	}
	if r.Validate() != nil {
		return rp.RemoteCIRecord{}, ErrCIExecutionLifecycle
	}
	return r, nil
}
