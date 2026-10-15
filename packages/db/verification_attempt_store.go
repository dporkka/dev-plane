package db

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "strings"

    protocol "github.com/ai-dev-control-plane/repoprotocol"
)

// InsertVerificationAttempt is intentionally insert-only: an attempt ID cannot
// replace, edit, or silently upgrade previously concluded verification evidence.
func (db *DB) InsertVerificationAttempt(ctx context.Context, attempt protocol.VerificationAttempt) error {
    if err := attempt.Validate(); err != nil {
        return fmt.Errorf("invalid verification attempt: %w", err)
    }
    raw, err := json.Marshal(attempt)
    if err != nil { return fmt.Errorf("encode verification attempt: %w", err) }
    query := `INSERT INTO verification_attempts (
        id, work_item_id, repository, source_head_sha, target_base_sha,
        candidate_commit_sha, policy_digest, payload, completed_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
    if db.Driver == "postgres" {
        query = rebindPostgres(query)
    }
    _, err = db.ExecContext(ctx, query,
        attempt.ID, attempt.WorkItemID, attempt.Repository,
        attempt.SourceHeadSHA, attempt.TargetBaseSHA,
        attempt.CandidateCommitSHA, attempt.PolicyDigest,
        raw, attempt.CompletedAt.UTC(),
    )
    if err != nil { return fmt.Errorf("insert verification attempt %s: %w", attempt.ID, err) }
    return nil
}

func (db *DB) GetVerificationAttempt(ctx context.Context, id string) (protocol.VerificationAttempt, error) {
    if strings.TrimSpace(id) == "" {
        return protocol.VerificationAttempt{}, errors.New("verification attempt id is required")
    }
    query := "SELECT payload FROM verification_attempts WHERE id = ?"
    if db.Driver == "postgres" { query = rebindPostgres(query) }
    var raw []byte
    if err := db.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
        return protocol.VerificationAttempt{}, fmt.Errorf("get verification attempt %s: %w", id, err)
    }
    var attempt protocol.VerificationAttempt
    if err := json.Unmarshal(raw, &attempt); err != nil {
        return protocol.VerificationAttempt{}, fmt.Errorf("decode verification attempt %s: %w", id, err)
    }
    if err := attempt.Validate(); err != nil {
        return protocol.VerificationAttempt{}, fmt.Errorf("stored verification attempt %s is invalid: %w", id, err)
    }
    return attempt, nil
}
