package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/execution"
	"github.com/ai-dev-control-plane/models"
)

func (r *Runner) updateRunStatus(ctx context.Context, runID, status string, summary *string) error {
	if r.db == nil {
		return errors.New("run status database is not configured")
	}
	now := time.Now().UTC()
	var startedAt interface{}
	if status == models.AgentRunStatusRunning {
		startedAt = now
	}
	query := `
        UPDATE agent_runs
        SET status = $1, started_at = COALESCE($2, started_at), updated_at = $3
        WHERE id = $4
    `
	if status == models.AgentRunStatusRunning {
		// Exactly one worker may claim a run. An old delivery cannot resurrect
		// an already running, canceled, completed or failed run.
		query += ` AND status IN ('pending', 'queued', 'paused')`
	}
	result, err := r.db.ExecContext(ctx, query, status, startedAt, now, runID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("confirm run status update: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: cannot transition run %s to %s", ErrRunTerminalStateChanged, runID, status)
	}
	return nil
}

func (r *Runner) failRun(ctx context.Context, runID string, errorMsg string) error {
	failure := fmt.Errorf("run %s failed: %s", runID, errorMsg)
	r.logger.Error("agent run failed", "run_id", runID, "error", errorMsg)
	if r.db == nil {
		return errors.Join(failure, errors.New("failed run cannot be persisted without a database"))
	}
	// Cleanup must have a bounded chance to persist even if caller canceled.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	result, err := r.db.ExecContext(persistCtx, `
        UPDATE agent_runs
        SET status = $1, error_message = $2, completed_at = $3, updated_at = $3
        WHERE id = $4 AND status NOT IN ('failed', 'completed', 'cancelled')
    `, models.AgentRunStatusFailed, errorMsg, now, runID)
	if err != nil {
		return errors.Join(failure, fmt.Errorf("persist failed run status: %w", err))
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Join(failure, fmt.Errorf("confirm failed run status: %w", err))
	}
	if rows != 1 {
		return errors.Join(failure, ErrRunTerminalStateChanged)
	}

	// A terminal event must not be published unless the run's terminal state
	// was persisted. Event delivery itself still requires an outbox for a hard
	// at-least-once guarantee; this routine never fabricates a DB commit.
	_ = r.publishEvent(persistCtx, events.StreamRuns, fmt.Sprintf("runs.%s.failed", runID), map[string]any{
		"run_id": runID, "status": models.AgentRunStatusFailed,
		"error": errorMsg, "timestamp": now,
	})
	_ = r.publishEvent(persistCtx, events.StreamAgents, events.AgentRunFailed, map[string]any{
		"run_id": runID, "status": models.AgentRunStatusFailed,
		"error": errorMsg, "timestamp": now,
	})
	return failure
}

func (r *Runner) pauseRun(ctx context.Context, runID string, reason string) error {
	r.logger.Info("agent run paused", "run_id", runID, "reason", reason)

	if r.db != nil {
		now := time.Now().UTC()
		_, _ = r.db.ExecContext(ctx, `
			UPDATE agent_runs
			SET status = $1, error_message = $2, updated_at = $3
			WHERE id = $4
		`, models.AgentRunStatusPaused, reason, now, runID)
	}

	_ = r.publishEvent(ctx, events.StreamRuns, fmt.Sprintf("runs.%s.paused", runID), map[string]any{
		"run_id":    runID,
		"status":    models.AgentRunStatusPaused,
		"reason":    reason,
		"timestamp": time.Now().UTC(),
	})

	return nil
}

func capabilityApprovalMetadata(decision *capabilityDecisionError) ([]byte, error) {
	if decision == nil || decision.result == nil {
		return nil, fmt.Errorf("capability decision is required")
	}

	metadata := map[string]any{
		"tool_name":  decision.toolName,
		"operation":  decision.operation,
		"resource":   decision.resource,
		"effect":     decision.result.Effect,
		"risk_level": decision.result.RiskLevel,
		"reason":     decision.result.Reason,
	}
	if grant := decision.result.RequestedGrant; grant != nil {
		manifest, err := execution.NewManifest(*grant)
		if err != nil {
			return nil, fmt.Errorf("build authority manifest: %w", err)
		}
		digest, err := manifest.Digest()
		if err != nil {
			return nil, fmt.Errorf("digest authority manifest: %w", err)
		}
		metadata["authority_grant"] = map[string]string{
			"operation": grant.Operation,
			"resource":  grant.Resource,
			"revision":  grant.Revision,
		}
		metadata["authority_digest"] = digest
	}

	return json.Marshal(metadata)
}

func (r *Runner) requestCapabilityApproval(ctx context.Context, run *models.AgentRun, task *models.Task, decision *capabilityDecisionError) error {
	if r.db == nil || task == nil || decision == nil || decision.result == nil {
		return nil
	}

	approvalID := uuid.New().String()
	now := time.Now().UTC()
	metadata, err := capabilityApprovalMetadata(decision)
	if err != nil {
		return fmt.Errorf("marshal approval metadata: %w", err)
	}

	var agentRunID any
	if run != nil {
		agentRunID = run.ID
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO approvals (
			id, task_id, agent_run_id, approval_type, requested_by, requested_at,
			metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $6, $6)
	`, approvalID, task.ID, agentRunID, "capability:"+decision.operation, task.CreatedBy, now, string(metadata))
	if err != nil {
		return err
	}

	_ = r.publishEvent(ctx, events.StreamRuns, events.ApprovalRequested, map[string]any{
		"approval_id": approvalID,
		"task_id":     task.ID,
		"run_id":      agentRunID,
		"operation":   decision.operation,
		"resource":    decision.resource,
		"timestamp":   now,
	})

	return nil
}

func (r *Runner) requestModelApproval(ctx context.Context, run *models.AgentRun, task *models.Task, reason string) error {
	if r.db == nil || task == nil {
		return nil
	}

	approvalID := uuid.New().String()
	now := time.Now().UTC()
	metadata, err := json.Marshal(map[string]any{
		"source":     "model_request",
		"reason":     reason,
		"agent_role": "",
	})
	if err != nil {
		return fmt.Errorf("marshal approval metadata: %w", err)
	}

	var agentRunID any
	if run != nil {
		agentRunID = run.ID
		var metadataMap map[string]any
		if err := json.Unmarshal(metadata, &metadataMap); err == nil {
			metadataMap["agent_role"] = run.AgentRole
			if encoded, err := json.Marshal(metadataMap); err == nil {
				metadata = encoded
			}
		}
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO approvals (
			id, task_id, agent_run_id, approval_type, requested_by, requested_at,
			metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $6, $6)
	`, approvalID, task.ID, agentRunID, models.ApprovalTypeRiskyAction, task.CreatedBy, now, string(metadata))
	if err != nil {
		return err
	}

	_ = r.publishEvent(ctx, events.StreamRuns, events.ApprovalRequested, map[string]any{
		"approval_id":   approvalID,
		"task_id":       task.ID,
		"run_id":        agentRunID,
		"approval_type": models.ApprovalTypeRiskyAction,
		"reason":        reason,
		"timestamp":     now,
	})

	return nil
}

// ErrRunTerminalStateChanged means a different actor has already changed the
// run's status. A late agent completion must never undo a cancellation or fail.
var ErrRunTerminalStateChanged = errors.New("run is no longer running")

// completeRunWithRecovery attempts bounded persistence using a fresh, short
// cleanup context even if the calling request has already been canceled.
// A completion is never announced until its state has actually been stored.
// If saving completion proves impossible, a conditional failed transition makes
// the run eligible for manual recovery, but only if that transition succeeds.
// A prolonged database outage still requires a separately leased reconciler.
func (r *Runner) completeRunWithRecovery(ctx context.Context, runID, summary string, state *RunState) error {
	if r.db == nil {
		return errors.New("cannot persist completed run without a database")
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	var completionErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			pause := time.Duration(attempt*40) * time.Millisecond
			timer := time.NewTimer(pause)
			select {
			case <-persistCtx.Done():
				timer.Stop()
				return errors.Join(fmt.Errorf("persist run completion: %w", completionErr), persistCtx.Err())
			case <-timer.C:
			}
		}
		completionErr = r.updateRunCompletion(persistCtx, runID, models.AgentRunStatusCompleted, summary, state)
		if completionErr == nil {
			return nil
		}
		if errors.Is(completionErr, ErrRunTerminalStateChanged) {
			return fmt.Errorf("persist run completion: %w", completionErr)
		}
	}

	// We must not emit a failure event for a transition that was not stored.
	// A conditional update also protects cancellations and newer terminal states.
	now := time.Now().UTC()
	result, err := r.db.ExecContext(persistCtx, `
        UPDATE agent_runs
        SET status = $1, error_message = $2, completed_at = $3, updated_at = $3
        WHERE id = $4 AND status = 'running'
    `, models.AgentRunStatusFailed, "completion persistence failed; manual recovery required", now, runID)
	if err != nil {
		return errors.Join(fmt.Errorf("persist run completion: %w", completionErr), fmt.Errorf("persist fallback failed state: %w", err))
	}
	count, err := result.RowsAffected()
	if err != nil {
		return errors.Join(fmt.Errorf("persist run completion: %w", completionErr), fmt.Errorf("confirm fallback failed state: %w", err))
	}
	if count != 1 {
		return errors.Join(fmt.Errorf("persist run completion: %w", completionErr), ErrRunTerminalStateChanged)
	}
	r.logger.Error("completion persistence failed; run marked failed for manual recovery", "run_id", runID, "error", completionErr)
	// An accurate failure event is permitted only after the database confirmed
	// the failed terminal transition. Failed publication remains best-effort.
	_ = r.publishEvent(persistCtx, events.StreamRuns, fmt.Sprintf("runs.%s.failed", runID), map[string]any{
		"run_id": runID, "status": models.AgentRunStatusFailed,
		"error": "completion persistence failed", "timestamp": now,
	})
	_ = r.publishEvent(persistCtx, events.StreamAgents, events.AgentRunFailed, map[string]any{
		"run_id": runID, "status": models.AgentRunStatusFailed,
		"error": "completion persistence failed", "timestamp": now,
	})
	return fmt.Errorf("run %s marked failed after completion persistence error: %w", runID, completionErr)
}

func (r *Runner) updateRunCompletion(ctx context.Context, runID, status, summary string, state *RunState) error {
	if r.db == nil {
		return errors.New("completion database is not configured")
	}
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, `
        UPDATE agent_runs
        SET status = $1, summary = $2, total_cost = $3,
            completed_at = $4, updated_at = $4
        WHERE id = $5 AND status = 'running'
    `, status, summary, state.CostSoFar, now, runID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("confirm run completion update: %w", err)
	}
	if count == 1 {
		return nil
	}
	if count != 0 {
		return fmt.Errorf("unexpected completion row count %d", count)
	}
	// The previous attempt may have committed but lost the DB response. Confirm
	// its stored summary and metered cost before returning idempotent success.
	var storedStatus string
	var storedSummary sql.NullString
	var storedCost sql.NullFloat64
	err = r.db.QueryRowContext(ctx, `
        SELECT status, summary, total_cost FROM agent_runs WHERE id = $1
    `, runID).Scan(&storedStatus, &storedSummary, &storedCost)
	if err != nil {
		return fmt.Errorf("check previously committed completion: %w", err)
	}
	if storedStatus == status && storedSummary.Valid && storedSummary.String == summary && storedCost.Valid && math.Abs(storedCost.Float64-state.CostSoFar) < 0.000001 {
		return nil
	}
	return fmt.Errorf("%w: persisted status %q", ErrRunTerminalStateChanged, storedStatus)
}
