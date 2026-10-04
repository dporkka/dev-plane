package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	models "github.com/ai-dev-control-plane/models"
)

// PutGoalProof persists one immutable proof reference. Admission is serialized
// against goal proof-epoch rotation by locking the goal row in the same
// transaction. Historical proof remains stored after later rotations, but new
// proof for a stale epoch is rejected.
func (db *DB) PutGoalProof(ctx context.Context, goalID string, proof models.GoalProof) error {
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return errors.New("goal id is required")
	}
	proof.CriterionID = strings.TrimSpace(proof.CriterionID)
	proof.CriterionDigest = strings.TrimSpace(proof.CriterionDigest)
	proof.EvidenceID = strings.TrimSpace(proof.EvidenceID)
	proof.SubjectRevision = strings.TrimSpace(proof.SubjectRevision)
	proof.ProofEpoch = strings.TrimSpace(proof.ProofEpoch)
	if proof.CriterionID == "" {
		return errors.New("goal proof criterion_id is required")
	}
	if proof.CriterionDigest == "" {
		return errors.New("goal proof criterion_digest is required")
	}
	if proof.EvidenceID == "" {
		return errors.New("goal proof evidence_id is required")
	}
	if proof.SubjectRevision == "" {
		return errors.New("goal proof subject_revision is required")
	}
	if proof.ProofEpoch == "" {
		return errors.New("goal proof proof_epoch is required")
	}
	if proof.Status != models.GoalProofPassed && proof.Status != models.GoalProofFailed {
		return fmt.Errorf("goal proof has invalid status %q", proof.Status)
	}
	if proof.ObservedAt.IsZero() {
		return errors.New("goal proof observed_at is required")
	}

	return db.WithTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		goal, found, err := db.getGoalTx(ctx, tx, goalID, true)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("get goal %s: %w", goalID, sql.ErrNoRows)
		}
		if proof.ProofEpoch != goal.ProofEpoch {
			return fmt.Errorf("goal proof epoch is stale: proof=%s goal=%s", proof.ProofEpoch, goal.ProofEpoch)
		}

		criterion, ok := goalCriterion(goal, proof.CriterionID)
		if !ok {
			return fmt.Errorf("goal proof criterion %q is not part of goal %s", proof.CriterionID, goalID)
		}
		if proof.CriterionDigest != criterion.Digest() {
			return fmt.Errorf("goal proof criterion digest is stale for %q", proof.CriterionID)
		}

		existing, found, err := db.getGoalProofTx(ctx, tx, goalID, proof.CriterionID, proof.EvidenceID)
		if err != nil {
			return err
		}
		if found {
			if sameGoalProof(existing, proof) {
				return nil
			}
			return fmt.Errorf("goal proof evidence identity conflict for %s/%s/%s", goalID, proof.CriterionID, proof.EvidenceID)
		}

		payload, err := json.Marshal(proof)
		if err != nil {
			return fmt.Errorf("marshal goal proof: %w", err)
		}
		query := `
			INSERT INTO goal_proofs (
				goal_id, criterion_id, criterion_digest, evidence_id,
				subject_revision, proof_epoch, status, observed_at, payload
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		if db.Driver == "postgres" {
			query = rebindPostgres(query)
		}
		if _, err := tx.ExecContext(
			ctx,
			query,
			goalID,
			proof.CriterionID,
			proof.CriterionDigest,
			proof.EvidenceID,
			proof.SubjectRevision,
			proof.ProofEpoch,
			string(proof.Status),
			proof.ObservedAt,
			payload,
		); err != nil {
			return fmt.Errorf("put goal proof %s/%s/%s: %w", goalID, proof.CriterionID, proof.EvidenceID, err)
		}
		return nil
	})
}

// ListGoalProofs returns all proof history for a goal. Evaluation is responsible
// for treating records from previous proof epochs or criterion revisions as
// stale, preserving auditability without allowing stale completion evidence.
func (db *DB) ListGoalProofs(ctx context.Context, goalID string) ([]models.GoalProof, error) {
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return nil, errors.New("goal id is required")
	}
	query := `
		SELECT payload
		FROM goal_proofs
		WHERE goal_id = ?
		ORDER BY observed_at, criterion_id, evidence_id
	`
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	rows, err := db.QueryContext(ctx, query, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal proofs %s: %w", goalID, err)
	}
	defer rows.Close()

	proofs := make([]models.GoalProof, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan goal proof %s: %w", goalID, err)
		}
		var proof models.GoalProof
		if err := json.Unmarshal(raw, &proof); err != nil {
			return nil, fmt.Errorf("decode goal proof %s: %w", goalID, err)
		}
		proofs = append(proofs, proof)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goal proofs %s: %w", goalID, err)
	}
	return proofs, nil
}

func (db *DB) getGoalProofTx(ctx context.Context, tx *sql.Tx, goalID, criterionID, evidenceID string) (models.GoalProof, bool, error) {
	query := `
		SELECT payload
		FROM goal_proofs
		WHERE goal_id = ? AND criterion_id = ? AND evidence_id = ?
	`
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, query, goalID, criterionID, evidenceID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.GoalProof{}, false, nil
		}
		return models.GoalProof{}, false, fmt.Errorf("get goal proof %s/%s/%s: %w", goalID, criterionID, evidenceID, err)
	}
	var proof models.GoalProof
	if err := json.Unmarshal(raw, &proof); err != nil {
		return models.GoalProof{}, false, fmt.Errorf("decode goal proof %s/%s/%s: %w", goalID, criterionID, evidenceID, err)
	}
	return proof, true, nil
}

func goalCriterion(goal models.Goal, criterionID string) (models.GoalCriterion, bool) {
	criterionID = strings.TrimSpace(criterionID)
	for _, criterion := range goal.SuccessCriteria {
		if strings.TrimSpace(criterion.ID) == criterionID {
			return criterion, true
		}
	}
	return models.GoalCriterion{}, false
}

func sameGoalProof(a, b models.GoalProof) bool {
	return a.CriterionID == b.CriterionID &&
		a.CriterionDigest == b.CriterionDigest &&
		a.EvidenceID == b.EvidenceID &&
		a.SubjectRevision == b.SubjectRevision &&
		a.ProofEpoch == b.ProofEpoch &&
		a.Status == b.Status &&
		a.ObservedAt.Equal(b.ObservedAt)
}
