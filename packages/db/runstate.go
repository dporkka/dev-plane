package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ai-dev-control-plane/models"
)

var (
	ErrRunNotFound       = errors.New("agent run not found")
	ErrInvalidTransition = errors.New("invalid agent run transition")
	ErrStaleRunState     = errors.New("stale agent run state")
)

type AgentRunTransition struct {
	RunID        string
	ToStatus     string
	Outcome      *models.Outcome
	ErrorMessage *string
	ClearError   bool
	Summary      *string
	TotalCost    *float64
	StartedAt    *time.Time
	CompletedAt  *time.Time
}

type AgentRunTransitionResult struct {
	PreviousStatus string
	Status         string
	StateVersion   int64
}

func TransitionAgentRun(ctx context.Context, database *sql.DB, req AgentRunTransition) (AgentRunTransitionResult, error) {
	if database == nil {
		return AgentRunTransitionResult{}, errors.New("database is nil")
	}
	var currentStatus string
	var currentVersion int64
	err := database.QueryRowContext(ctx, `
		SELECT status, state_version
		FROM agent_runs
		WHERE id = $1
	`, req.RunID).Scan(&currentStatus, &currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRunTransitionResult{}, ErrRunNotFound
	}
	if err != nil {
		return AgentRunTransitionResult{}, fmt.Errorf("load run state: %w", err)
	}
	if !models.CanTransitionAgentRunStatus(currentStatus, req.ToStatus) {
		return AgentRunTransitionResult{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, currentStatus, req.ToStatus)
	}

	var outcome any
	if req.Outcome != nil && req.Outcome.Valid() {
		outcome = string(*req.Outcome)
	}
	var errorMessage any
	if req.ErrorMessage != nil {
		errorMessage = *req.ErrorMessage
	}
	var summary any
	if req.Summary != nil {
		summary = *req.Summary
	}
	var totalCost any
	if req.TotalCost != nil {
		totalCost = *req.TotalCost
	}
	now := time.Now().UTC()

	var nextVersion int64
	err = database.QueryRowContext(ctx, `
		UPDATE agent_runs
		SET status = $1,
		    state_version = state_version + 1,
		    outcome = COALESCE($2, outcome),
		    error_message = CASE WHEN $3 THEN NULL ELSE COALESCE($4, error_message) END,
		    summary = COALESCE($5, summary),
		    total_cost = COALESCE($6, total_cost),
		    started_at = COALESCE($7, started_at),
		    completed_at = COALESCE($8, completed_at),
		    updated_at = $9
		WHERE id = $10 AND status = $11 AND state_version = $12
		RETURNING state_version
	`, req.ToStatus, outcome, req.ClearError, errorMessage, summary, totalCost,
		req.StartedAt, req.CompletedAt, now, req.RunID, currentStatus, currentVersion,
	).Scan(&nextVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRunTransitionResult{}, ErrStaleRunState
	}
	if err != nil {
		return AgentRunTransitionResult{}, fmt.Errorf("transition run state: %w", err)
	}
	return AgentRunTransitionResult{
		PreviousStatus: currentStatus,
		Status:         req.ToStatus,
		StateVersion:   nextVersion,
	}, nil
}

type AgentRunLifecycleEvent struct {
	RunID                string
	Status               string
	StateVersion         int64
	RequireLatestAttempt bool
	ClaimTTL             time.Duration
}

type AgentRunLifecycleClaim struct {
	RunID        string
	Status       string
	StateVersion int64
	Claimed      bool
}

func ClaimAgentRunLifecycleEvent(ctx context.Context, database *sql.DB, event AgentRunLifecycleEvent) (AgentRunLifecycleClaim, error) {
	if database == nil {
		return AgentRunLifecycleClaim{}, errors.New("database is nil")
	}
	if event.ClaimTTL <= 0 {
		event.ClaimTTL = 5 * time.Minute
	}

	var currentStatus string
	var currentVersion int64
	var taskID string
	var attempt int
	err := database.QueryRowContext(ctx, `
		SELECT status, state_version, task_id, attempt
		FROM agent_runs
		WHERE id = $1
	`, event.RunID).Scan(&currentStatus, &currentVersion, &taskID, &attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRunLifecycleClaim{}, ErrRunNotFound
	}
	if err != nil {
		return AgentRunLifecycleClaim{}, fmt.Errorf("load lifecycle event run: %w", err)
	}

	version := event.StateVersion
	if version <= 0 {
		version = currentVersion
	}
	if currentStatus != event.Status || currentVersion != version {
		return AgentRunLifecycleClaim{RunID: event.RunID, Status: currentStatus, StateVersion: currentVersion}, nil
	}
	if event.RequireLatestAttempt {
		var newer int
		if err := database.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM agent_runs
			WHERE task_id = $1 AND attempt > $2
		`, taskID, attempt).Scan(&newer); err != nil {
			return AgentRunLifecycleClaim{}, fmt.Errorf("check newer run attempt: %w", err)
		}
		if newer > 0 {
			return AgentRunLifecycleClaim{RunID: event.RunID, Status: currentStatus, StateVersion: currentVersion}, nil
		}
	}

	now := time.Now().UTC()
	staleBefore := now.Add(-event.ClaimTTL)
	result, err := database.ExecContext(ctx, `
		UPDATE agent_runs
		SET processing_event_version = $1, event_claimed_at = $2
		WHERE id = $3
		  AND status = $4
		  AND state_version = $1
		  AND processed_event_version < $1
		  AND (
		    processing_event_version < $1
		    OR event_claimed_at IS NULL
		    OR event_claimed_at < $5
		  )
	`, version, now, event.RunID, event.Status, staleBefore)
	if err != nil {
		return AgentRunLifecycleClaim{}, fmt.Errorf("claim lifecycle event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return AgentRunLifecycleClaim{}, fmt.Errorf("check lifecycle claim: %w", err)
	}
	return AgentRunLifecycleClaim{
		RunID:        event.RunID,
		Status:       event.Status,
		StateVersion: version,
		Claimed:      rows == 1,
	}, nil
}

func CompleteAgentRunLifecycleEvent(ctx context.Context, database *sql.DB, claim AgentRunLifecycleClaim) error {
	if !claim.Claimed {
		return nil
	}
	result, err := database.ExecContext(ctx, `
		UPDATE agent_runs
		SET processed_event_version = CASE
		      WHEN processed_event_version < $1 THEN $1
		      ELSE processed_event_version
		    END,
		    processing_event_version = 0,
		    event_claimed_at = NULL
		WHERE id = $2 AND processing_event_version = $1
	`, claim.StateVersion, claim.RunID)
	if err != nil {
		return fmt.Errorf("complete lifecycle event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check lifecycle completion: %w", err)
	}
	if rows == 0 {
		return ErrStaleRunState
	}
	return nil
}

func ReleaseAgentRunLifecycleEvent(ctx context.Context, database *sql.DB, claim AgentRunLifecycleClaim) error {
	if !claim.Claimed {
		return nil
	}
	_, err := database.ExecContext(ctx, `
		UPDATE agent_runs
		SET processing_event_version = 0, event_claimed_at = NULL
		WHERE id = $1 AND processing_event_version = $2 AND processed_event_version < $2
	`, claim.RunID, claim.StateVersion)
	if err != nil {
		return fmt.Errorf("release lifecycle event: %w", err)
	}
	return nil
}
