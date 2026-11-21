package agentrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/execution"
	"github.com/ai-dev-control-plane/models"
)

func (r *Runner) updateRunStatus(ctx context.Context, runID, status string, summary *string) error {
	now := time.Now().UTC()
	var startedAt interface{}
	if status == models.AgentRunStatusRunning {
		startedAt = now
	}

	return r.writeRunStatus(ctx, runID, false,
		"status = $1, started_at = COALESCE($2, started_at), updated_at = $3", status, startedAt, now)
}

func (r *Runner) failRun(ctx context.Context, runID string, errorMsg string) error {
	if err := r.writeRunStatus(ctx, runID, true,
		"status = $1, error_message = $2, completed_at = $3, updated_at = $3", models.AgentRunStatusFailed, errorMsg, time.Now().UTC()); err != nil {
		return err
	}

	r.logger.Error("agent run failed", "run_id", runID, "error", errorMsg)

	// Publish run.failed event
	_ = r.publishEvent(ctx, events.StreamRuns, fmt.Sprintf("runs.%s.failed", runID), map[string]any{
		"run_id":    runID,
		"status":    models.AgentRunStatusFailed,
		"error":     errorMsg,
		"timestamp": time.Now().UTC(),
	})
	_ = r.publishEvent(ctx, events.StreamAgents, events.AgentRunFailed, map[string]any{
		"run_id":    runID,
		"status":    models.AgentRunStatusFailed,
		"error":     errorMsg,
		"timestamp": time.Now().UTC(),
	})

	return fmt.Errorf("run %s failed: %s", runID, errorMsg)
}

func (r *Runner) pauseRun(ctx context.Context, runID string, reason string) error {
	if err := r.writeRunStatus(ctx, runID, true,
		"status = $1, error_message = $2, updated_at = $3", models.AgentRunStatusPaused, reason, time.Now().UTC()); err != nil {
		return err
	}

	r.logger.Info("agent run paused", "run_id", runID, "reason", reason)

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

func (r *Runner) updateRunCompletion(ctx context.Context, runID, status, summary string, state *RunState) error {
	return r.writeRunStatus(ctx, runID, false,
		"status = $1, summary = $2, total_cost = $3, completed_at = $4, updated_at = $4",
		status, summary, state.CostSoFar, time.Now().UTC())
}

// writeRunStatus takes a SET clause from a runner constant; all values are bound
// separately. bestEffort preserves legacy pause/fail handling without leases.
func (r *Runner) writeRunStatus(ctx context.Context, runID string, bestEffort bool, set string, args ...any) error {
	token, err := execution.RunStatusLeaseToken(ctx, runID)
	if err != nil {
		return fmt.Errorf("verify run execution ownership: %w", err)
	}
	if r.db == nil {
		if token != "" {
			return fmt.Errorf("%w: run status database unavailable", execution.ErrRunLeaseUnavailable)
		}
		return nil
	}

	idParam := fmt.Sprintf("$%d", len(args)+1)
	query := "UPDATE agent_runs SET " + set + " WHERE id = " + idParam
	args = append(args, runID)
	if token == "" {
		_, err := r.db.ExecContext(ctx, query, args...)
		if bestEffort {
			return nil
		}
		return err
	}

	tokenParam := fmt.Sprintf("$%d", len(args)+1)
	args = append(args, token)
	conditions := "execution_lease_token = " + tokenParam + `
		AND status IN ('admitting', 'running')
		AND recovery_review_required_at IS NULL`
	if _, postgres := r.db.Driver().(*pq.Driver); postgres {
		// Check the deadline with the database clock after acquiring the row
		// lock, so time spent waiting for a concurrent transaction counts.
		query = `WITH locked_run AS MATERIALIZED (
			SELECT id, status, execution_lease_token, execution_lease_expires_at, recovery_review_required_at
			FROM agent_runs WHERE id = ` + idParam + ` FOR UPDATE
		) ` + query + ` AND id IN (
			SELECT id FROM locked_run WHERE ` + conditions + `
			AND execution_lease_expires_at > clock_timestamp()
		)`
	} else {
		// SQLite serializes writers. Normalize the timestamp formats before
		// comparing the stored deadline against SQLite's UTC clock.
		query += " AND " + conditions + " AND julianday(execution_lease_expires_at) > julianday('now')"
	}

	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("persist leased run status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("confirm leased run status: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: status update rejected for run %s", execution.ErrRunLeaseUnavailable, runID)
	}
	return nil
}
