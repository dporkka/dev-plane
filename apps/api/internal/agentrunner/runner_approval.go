package agentrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/events"
	runfailure "github.com/ai-dev-control-plane/failure"
	"github.com/ai-dev-control-plane/models"
)

func (r *Runner) updateRunStatus(ctx context.Context, runID, status string, summary *string) error {
	if r.db == nil {
		return nil
	}

	now := time.Now().UTC()
	var startedAt interface{}
	if status == models.AgentRunStatusRunning {
		startedAt = now
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = $1, started_at = COALESCE($2, started_at), updated_at = $3
		WHERE id = $4
	`, status, startedAt, now, runID)
	return err
}

func (r *Runner) failRun(ctx context.Context, runID string, errorMsg string) error {
	classification := runfailure.Classify(runfailure.Signal{
		Stage:  "execution",
		Detail: errorMsg,
	})
	return r.failRunClassifiedWithData(ctx, runID, "", errorMsg, nil, classification)
}

func (r *Runner) failRunWithData(
	ctx context.Context,
	runID string,
	taskID string,
	errorMsg string,
	data any,
) error {
	classification := runfailure.Classify(runfailure.Signal{
		Stage:  "execution",
		Detail: errorMsg,
	})
	return r.failRunClassifiedWithData(ctx, runID, taskID, errorMsg, data, classification)
}

func (r *Runner) failRunClassified(
	ctx context.Context,
	runID string,
	errorMsg string,
	classification runfailure.Classification,
) error {
	return r.failRunClassifiedWithData(ctx, runID, "", errorMsg, nil, classification)
}

func (r *Runner) failRunClassifiedWithData(
	ctx context.Context,
	runID string,
	taskID string,
	errorMsg string,
	data any,
	classification runfailure.Classification,
) error {
	r.logger.Error(
		"agent run failed",
		"run_id", runID,
		"error", errorMsg,
		"failure_category", classification.Category,
		"retryable", classification.Retryable,
		"disposition", classification.Disposition,
	)

	if r.db != nil {
		now := time.Now().UTC()
		metadata := r.runMetadataWithFailure(ctx, runID, classification)
		_, _ = r.db.ExecContext(ctx, `
			UPDATE agent_runs
			SET status = $1, error_message = $2, metadata = $3,
			    completed_at = $4, updated_at = $4
			WHERE id = $5
		`, models.AgentRunStatusFailed, errorMsg, metadata, now, runID)
	}

	failurePayload := map[string]any{
		"taxonomy":    classification.Taxonomy,
		"category":    classification.Category,
		"retryable":   classification.Retryable,
		"disposition": classification.Disposition,
		"stage":       classification.Stage,
		"source":      classification.Source,
	}
	payload := map[string]any{
		"run_id":    runID,
		"status":    models.AgentRunStatusFailed,
		"error":     errorMsg,
		"failure":   failurePayload,
		"timestamp": time.Now().UTC(),
	}
	if taskID != "" {
		payload["task_id"] = taskID
	}
	if data != nil {
		payload["data"] = data
	}

	_ = r.publishEvent(ctx, events.StreamRuns, fmt.Sprintf("runs.%s.failed", runID), payload)
	_ = r.publishEvent(ctx, events.StreamAgents, events.AgentRunFailed, payload)

	return fmt.Errorf("run %s failed: %s", runID, errorMsg)
}

func (r *Runner) runMetadataWithFailure(
	ctx context.Context,
	runID string,
	classification runfailure.Classification,
) string {
	metadata := map[string]any{}
	var raw string
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT COALESCE(metadata, '{}') FROM agent_runs WHERE id = $1`,
		runID,
	).Scan(&raw); err == nil && raw != "" {
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
			r.logger.Warn("failed to decode run metadata before failure classification", "run_id", runID, "error", err)
			metadata = map[string]any{"legacy_metadata_raw": raw}
		}
	}
	metadata["failure"] = classification
	encoded, err := json.Marshal(metadata)
	if err != nil {
		r.logger.Warn("failed to encode classified run metadata", "run_id", runID, "error", err)
		return `{"failure":{"taxonomy":"run-failure-v1","category":"unknown","retryable":false,"disposition":"investigate"}}`
	}
	return string(encoded)
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

func (r *Runner) requestCapabilityApproval(ctx context.Context, run *models.AgentRun, task *models.Task, decision *capabilityDecisionError) error {
	if r.db == nil || task == nil || decision == nil || decision.result == nil {
		return nil
	}

	approvalID := uuid.New().String()
	now := time.Now().UTC()
	metadata, err := json.Marshal(map[string]any{
		"tool_name":  decision.toolName,
		"operation":  decision.operation,
		"resource":   decision.resource,
		"effect":     decision.result.Effect,
		"risk_level": decision.result.RiskLevel,
		"reason":     decision.result.Reason,
	})
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

func (r *Runner) updateRunCompletion(ctx context.Context, runID, status, summary string, state *RunState) error {
	if r.db == nil {
		return nil
	}

	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = $1, summary = $2, total_cost = $3,
		    completed_at = $4, updated_at = $4
		WHERE id = $5
	`, status, summary, state.CostSoFar, now, runID)
	return err
}
