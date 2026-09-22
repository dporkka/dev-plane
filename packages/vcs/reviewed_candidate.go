package vcs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrReviewedCandidateNotFound = errors.New("reviewed candidate not found")
	ErrReviewedCandidateImmutable = errors.New("reviewed candidate is immutable")
)

// ReviewedCandidate binds an immutable source revision to the review evidence
// that authorized it. PR creation must publish this revision, never whatever
// happens to be at the mutable workspace HEAD later.
type ReviewedCandidate struct {
	RunID        string            `json:"run_id"`
	TaskID       string            `json:"task_id"`
	WorkspaceID  string            `json:"workspace_id"`
	ReviewDigest string            `json:"review_digest"`
	Candidate    VerifiedCandidate `json:"candidate"`
	FrozenAt     time.Time         `json:"frozen_at"`
}

func (c ReviewedCandidate) Validate() error {
	if strings.TrimSpace(c.RunID) == "" {
		return fmt.Errorf("run id is required")
	}
	if strings.TrimSpace(c.TaskID) == "" {
		return fmt.Errorf("task id is required")
	}
	if strings.TrimSpace(c.WorkspaceID) == "" {
		return fmt.Errorf("workspace id is required")
	}
	if strings.TrimSpace(c.ReviewDigest) == "" {
		return fmt.Errorf("review digest is required")
	}
	if strings.TrimSpace(c.Candidate.Revision.CommitID) == "" {
		return fmt.Errorf("candidate commit id is required")
	}
	if strings.TrimSpace(c.Candidate.Branch) == "" {
		return fmt.Errorf("candidate branch is required")
	}
	if c.Candidate.Metadata.TaskID != "" && c.Candidate.Metadata.TaskID != c.TaskID {
		return fmt.Errorf("candidate task id %q does not match reviewed task %q", c.Candidate.Metadata.TaskID, c.TaskID)
	}
	if c.Candidate.Metadata.WorkspaceID != "" && c.Candidate.Metadata.WorkspaceID != c.WorkspaceID {
		return fmt.Errorf("candidate workspace id %q does not match reviewed workspace %q", c.Candidate.Metadata.WorkspaceID, c.WorkspaceID)
	}
	return nil
}

// SaveReviewedCandidate persists the first frozen candidate for a run. Replays
// are idempotent only when they describe exactly the same reviewed revision;
// a later attempt to replace the candidate fails closed.
func SaveReviewedCandidate(ctx context.Context, db *sql.DB, record ReviewedCandidate) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if record.FrozenAt.IsZero() {
		record.FrozenAt = time.Now().UTC()
	}
	meta := record.Candidate.Metadata
	if meta.TaskID == "" {
		meta.TaskID = record.TaskID
	}
	if meta.WorkspaceID == "" {
		meta.WorkspaceID = record.WorkspaceID
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO reviewed_candidates (
			run_id, task_id, workspace_id, candidate_commit_id, candidate_change_id,
			candidate_branch, candidate_changed, candidate_agent_id, review_digest, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (run_id) DO NOTHING
	`,
		record.RunID, record.TaskID, record.WorkspaceID,
		record.Candidate.Revision.CommitID, record.Candidate.Revision.ChangeID,
		record.Candidate.Branch, record.Candidate.Changed, meta.AgentID,
		record.ReviewDigest, record.FrozenAt,
	)
	if err != nil {
		return fmt.Errorf("save reviewed candidate: %w", err)
	}

	existing, err := LoadReviewedCandidate(ctx, db, record.RunID)
	if err != nil {
		return err
	}
	if !sameReviewedCandidate(existing, record) {
		return fmt.Errorf("%w: run %s is already bound to commit %s",
			ErrReviewedCandidateImmutable, record.RunID, existing.Candidate.Revision.CommitID)
	}
	return nil
}

func LoadReviewedCandidate(ctx context.Context, db *sql.DB, runID string) (ReviewedCandidate, error) {
	if db == nil {
		return ReviewedCandidate{}, fmt.Errorf("database is required")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ReviewedCandidate{}, fmt.Errorf("run id is required")
	}

	var out ReviewedCandidate
	var changeID, agentID sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT run_id, task_id, workspace_id, candidate_commit_id, candidate_change_id,
		       candidate_branch, candidate_changed, candidate_agent_id, review_digest, created_at
		FROM reviewed_candidates
		WHERE run_id = $1
	`, runID).Scan(
		&out.RunID, &out.TaskID, &out.WorkspaceID,
		&out.Candidate.Revision.CommitID, &changeID,
		&out.Candidate.Branch, &out.Candidate.Changed, &agentID,
		&out.ReviewDigest, &out.FrozenAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReviewedCandidate{}, fmt.Errorf("%w for run %s", ErrReviewedCandidateNotFound, runID)
		}
		return ReviewedCandidate{}, fmt.Errorf("load reviewed candidate: %w", err)
	}
	if changeID.Valid {
		out.Candidate.Revision.ChangeID = changeID.String
	}
	out.Candidate.Metadata = CandidateMetadata{
		TaskID:      out.TaskID,
		WorkspaceID: out.WorkspaceID,
	}
	if agentID.Valid {
		out.Candidate.Metadata.AgentID = agentID.String
	}
	return out, nil
}

func sameReviewedCandidate(a, b ReviewedCandidate) bool {
	return a.RunID == b.RunID &&
		a.TaskID == b.TaskID &&
		a.WorkspaceID == b.WorkspaceID &&
		a.ReviewDigest == b.ReviewDigest &&
		a.Candidate.Revision == b.Candidate.Revision &&
		a.Candidate.Branch == b.Candidate.Branch &&
		a.Candidate.Changed == b.Candidate.Changed
}
