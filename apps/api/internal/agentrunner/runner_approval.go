package agentrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
)

func (r *Runner) updateRunStatus(ctx context.Context, runID, status string, summary *string) (int64, error) {
	if r.db == nil {
		return 0, nil
	}

	now := time.Now().UTC()
	req := dbpkg.AgentRunTransition{
		RunID:    runID,
		ToStatus: status,
		Summary:  summary,
	}
	if status == models.AgentRunStatusRunning {
		req.StartedAt = &now
		req.ClearError = true
	}
	result, err := dbpkg.TransitionAgentRun(ctx, r.db, req)
	if err != nil {
		return 0, err
	}
	return result.StateVersion, nil
}

func (r *Runner) failRun(ctx context.Context, runID string, errorMsg string) error {
	r.logger.Error("agent run failed", "run_id", runID, "error", errorMsg)

	var stateVersion int64
	var taskID, agentRole string
	if r.db != nil {
		if run, err := r.loadAgentRun(ctx, runID); err == nil {
			taskID = run.TaskID
			agentRole = run.AgentRole
		}
		now := time.Now().UTC()
		outcome := models.OutcomeError
		result, err := dbpkg.TransitionAgentRun(ctx, r.db, dbpkg.AgentRunTransition{
			RunID:        runID,
			ToStatus:     models.AgentRunStatusFailed,
			Outcome:      &outcome,
			ErrorMessage: &errorMsg,
			CompletedAt:  &now,
		})
		if err != nil {
			return fmt.Errorf("fail run state transition: %w", err)
		}
		stateVersion = result.StateVersion
	}

	_ = r.publishEvent(ctx, events.StreamRuns, fmt.Sprintf("runs.%s.failed", runID), map[string]any{
		"run_id":        runID,
		"task_id":       taskID,
		"agent_role":    agentRole,
		"status":        models.AgentRunStatusFailed,
		"state_version": stateVersion,
		"error":         errorMsg,
		"timestamp":     time.Now().UTC(),
	})
	_ = r.publishEvent(ctx, events.StreamAgents, events.AgentRunFailed, map[string]any{
		"run_id":        runID,
		"task_id":       taskID,
		"agent_role":    agentRole,
		"status":        models.AgentRunStatusFailed,
		"state_version": stateVersion,
		"error":         errorMsg,
		"timestamp":     time.Now().UTC(),
	})

	return fmt.Errorf("run %s failed: %s", runID, errorMsg)
}

func (r *Runner) pauseRun(ctx context.Context, runID string, reason string) error {
	r.logger.Info("agent run paused", "run_id", runID, "reason", reason)

	var stateVersion int64
	if r.db != nil {
		result, err := dbpkg.TransitionAgentRun(ctx, r.db, dbpkg.AgentRunTransition{
			RunID:        runID,
			ToStatus:     models.AgentRunStatusPaused,
			ErrorMessage: &reason,
		})
		if err != nil {
			return fmt.Errorf("pause run state transition: %w", err)
		}
		stateVersion = result.StateVersion
	}

	_ = r.publishEvent(ctx, events.StreamRuns, fmt.Sprintf("runs.%s.paused", runID), map[string]any{
		"run_id":        runID,
		"status":        models.AgentRunStatusPaused,
		"state_version": stateVersion,
		"reason":        reason,
		"timestamp":     time.Now().UTC(),
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

func (r *Runner) updateRunCompletion(ctx context.Context, runID, status, summary string, state *RunState) (int64, error) {
	if r.db == nil {
		return 0, nil
	}

	now := time.Now().UTC()
	outcome := models.OutcomePassed
	totalCost := state.CostSoFar
	result, err := dbpkg.TransitionAgentRun(ctx, r.db, dbpkg.AgentRunTransition{
		RunID:       runID,
		ToStatus:    status,
		Outcome:     &outcome,
		Summary:     &summary,
		TotalCost:   &totalCost,
		CompletedAt: &now,
	})
	if err != nil {
		return 0, err
	}
	return result.StateVersion, nil
}
