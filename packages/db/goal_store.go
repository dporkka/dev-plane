package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	models "github.com/ai-dev-control-plane/models"
	"github.com/google/uuid"
)

// GoalWorkItemLink binds a durable goal to one work item and the exact subject
// revision that currently contributes to the goal's aggregate proof snapshot.
type GoalWorkItemLink struct {
	GoalID          string `json:"goal_id"`
	WorkItemID      string `json:"work_item_id"`
	SubjectRevision string `json:"subject_revision"`
}

// PutGoal creates or updates a durable goal. ProofEpoch is store-owned: callers
// cannot preserve stale proof by supplying an older epoch. Operational edits
// keep the current epoch, while objective or success-criterion changes rotate it.
func (db *DB) PutGoal(ctx context.Context, goal models.Goal) (models.Goal, error) {
	goal.ID = strings.TrimSpace(goal.ID)
	if goal.ID == "" {
		return models.Goal{}, errors.New("goal id is required")
	}
	if err := goal.Validate(); err != nil {
		return models.Goal{}, fmt.Errorf("validate goal: %w", err)
	}

	var stored models.Goal
	err := db.WithTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		previous, found, err := db.getGoalTx(ctx, tx, goal.ID, true)
		if err != nil {
			return err
		}

		if !found {
			goal.ProofEpoch = newGoalProofEpoch()
		} else {
			if previous.OrganizationID != goal.OrganizationID {
				return errors.New("goal organization_id is immutable")
			}
			if previous.CreatedBy != goal.CreatedBy {
				return errors.New("goal created_by is immutable")
			}
			if previous.ProofEpoch == "" || !sameGoalProofScope(previous, goal) {
				goal.ProofEpoch = newGoalProofEpoch()
			} else {
				goal.ProofEpoch = previous.ProofEpoch
			}
		}

		payload, err := json.Marshal(goal)
		if err != nil {
			return fmt.Errorf("marshal goal: %w", err)
		}

		query := `
			INSERT INTO goals (id, organization_id, created_by, status, proof_epoch, payload, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(id) DO UPDATE SET
				organization_id = excluded.organization_id,
				created_by = excluded.created_by,
				status = excluded.status,
				proof_epoch = excluded.proof_epoch,
				payload = excluded.payload,
				updated_at = CURRENT_TIMESTAMP
		`
		if db.Driver == "postgres" {
			query = rebindPostgres(query)
		}
		if _, err := tx.ExecContext(ctx, query, goal.ID, goal.OrganizationID, goal.CreatedBy, string(goal.Status), goal.ProofEpoch, payload); err != nil {
			return fmt.Errorf("put goal %s: %w", goal.ID, err)
		}
		stored = goal
		return nil
	})
	if err != nil {
		return models.Goal{}, err
	}
	return stored, nil
}

// GetGoal loads a durable goal by ID.
func (db *DB) GetGoal(ctx context.Context, id string) (models.Goal, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return models.Goal{}, errors.New("goal id is required")
	}
	query := "SELECT payload FROM goals WHERE id = ?"
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
		return models.Goal{}, fmt.Errorf("get goal %s: %w", id, err)
	}
	var goal models.Goal
	if err := json.Unmarshal(raw, &goal); err != nil {
		return models.Goal{}, fmt.Errorf("decode goal %s: %w", id, err)
	}
	return goal, nil
}

// LinkGoalWorkItem records the current immutable subject revision for a linked
// work item. A first link or changed revision rotates the goal proof epoch in the
// same transaction. Replaying the same revision is idempotent and keeps proof.
func (db *DB) LinkGoalWorkItem(ctx context.Context, goalID, workItemID, subjectRevision string) (models.Goal, error) {
	goalID = strings.TrimSpace(goalID)
	workItemID = strings.TrimSpace(workItemID)
	subjectRevision = strings.TrimSpace(subjectRevision)
	if goalID == "" {
		return models.Goal{}, errors.New("goal id is required")
	}
	if workItemID == "" {
		return models.Goal{}, errors.New("work item id is required")
	}
	if subjectRevision == "" {
		return models.Goal{}, errors.New("subject revision is required")
	}

	var stored models.Goal
	err := db.WithTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		goal, found, err := db.getGoalTx(ctx, tx, goalID, true)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("get goal %s: %w", goalID, sql.ErrNoRows)
		}

		if err := db.requireWorkItemTx(ctx, tx, workItemID); err != nil {
			return err
		}

		query := "SELECT subject_revision FROM goal_work_items WHERE goal_id = ? AND work_item_id = ?"
		if db.Driver == "postgres" {
			query = rebindPostgres(query)
		}
		var currentRevision string
		err = tx.QueryRowContext(ctx, query, goalID, workItemID).Scan(&currentRevision)
		switch {
		case err == nil && currentRevision == subjectRevision:
			stored = goal
			return nil
		case err == nil:
			update := `
				UPDATE goal_work_items
				SET subject_revision = ?, updated_at = CURRENT_TIMESTAMP
				WHERE goal_id = ? AND work_item_id = ?
			`
			if db.Driver == "postgres" {
				update = rebindPostgres(update)
			}
			if _, err := tx.ExecContext(ctx, update, subjectRevision, goalID, workItemID); err != nil {
				return fmt.Errorf("update goal work item %s/%s: %w", goalID, workItemID, err)
			}
		case errors.Is(err, sql.ErrNoRows):
			insert := `
				INSERT INTO goal_work_items (goal_id, work_item_id, subject_revision, updated_at)
				VALUES (?, ?, ?, CURRENT_TIMESTAMP)
			`
			if db.Driver == "postgres" {
				insert = rebindPostgres(insert)
			}
			if _, err := tx.ExecContext(ctx, insert, goalID, workItemID, subjectRevision); err != nil {
				return fmt.Errorf("link goal work item %s/%s: %w", goalID, workItemID, err)
			}
		default:
			return fmt.Errorf("get goal work item %s/%s: %w", goalID, workItemID, err)
		}

		goal.ProofEpoch = newGoalProofEpoch()
		payload, err := json.Marshal(goal)
		if err != nil {
			return fmt.Errorf("marshal rotated goal %s: %w", goalID, err)
		}
		updateGoal := `
			UPDATE goals
			SET proof_epoch = ?, payload = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`
		if db.Driver == "postgres" {
			updateGoal = rebindPostgres(updateGoal)
		}
		if _, err := tx.ExecContext(ctx, updateGoal, goal.ProofEpoch, payload, goalID); err != nil {
			return fmt.Errorf("rotate goal proof epoch %s: %w", goalID, err)
		}
		stored = goal
		return nil
	})
	if err != nil {
		return models.Goal{}, err
	}
	return stored, nil
}

// ListGoalWorkItems returns deterministic goal/work-item bindings.
func (db *DB) ListGoalWorkItems(ctx context.Context, goalID string) ([]GoalWorkItemLink, error) {
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return nil, errors.New("goal id is required")
	}
	query := `
		SELECT goal_id, work_item_id, subject_revision
		FROM goal_work_items
		WHERE goal_id = ?
		ORDER BY work_item_id
	`
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	rows, err := db.QueryContext(ctx, query, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal work items %s: %w", goalID, err)
	}
	defer rows.Close()

	links := make([]GoalWorkItemLink, 0)
	for rows.Next() {
		var link GoalWorkItemLink
		if err := rows.Scan(&link.GoalID, &link.WorkItemID, &link.SubjectRevision); err != nil {
			return nil, fmt.Errorf("scan goal work item %s: %w", goalID, err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goal work items %s: %w", goalID, err)
	}
	return links, nil
}

func (db *DB) getGoalTx(ctx context.Context, tx *sql.Tx, id string, lock bool) (models.Goal, bool, error) {
	query := "SELECT payload FROM goals WHERE id = ?"
	if lock && db.Driver == "postgres" {
		query += " FOR UPDATE"
	}
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Goal{}, false, nil
		}
		return models.Goal{}, false, fmt.Errorf("get goal %s: %w", id, err)
	}
	var goal models.Goal
	if err := json.Unmarshal(raw, &goal); err != nil {
		return models.Goal{}, false, fmt.Errorf("decode goal %s: %w", id, err)
	}
	return goal, true, nil
}

func (db *DB) requireWorkItemTx(ctx context.Context, tx *sql.Tx, id string) error {
	query := "SELECT 1 FROM work_items WHERE id = ?"
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	var exists int
	if err := tx.QueryRowContext(ctx, query, id).Scan(&exists); err != nil {
		return fmt.Errorf("get work item %s: %w", id, err)
	}
	return nil
}

func sameGoalProofScope(a, b models.Goal) bool {
	if strings.TrimSpace(a.Objective) != strings.TrimSpace(b.Objective) {
		return false
	}
	if len(a.SuccessCriteria) != len(b.SuccessCriteria) {
		return false
	}
	left := make([]string, 0, len(a.SuccessCriteria))
	right := make([]string, 0, len(b.SuccessCriteria))
	for _, criterion := range a.SuccessCriteria {
		left = append(left, strings.TrimSpace(criterion.ID)+"\x00"+criterion.Digest())
	}
	for _, criterion := range b.SuccessCriteria {
		right = append(right, strings.TrimSpace(criterion.ID)+"\x00"+criterion.Digest())
	}
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func newGoalProofEpoch() string {
	return "epoch:" + uuid.NewString()
}
