// Package handlers contains event handlers for the worker service.
//
// Run handlers process agent run lifecycle events:
//   - agents.run.completed -> consume mailbox handoffs or review completed work
//   - review.completed -> request human approval for PR creation
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/reviewer"
)

// RunHandler handles agent run lifecycle events.
type RunHandler struct {
	db       *sql.DB
	logger   *slog.Logger
	eventBus *events.Bus
	executor RunExecutor
	reviewer ReviewService
}

// RunExecutor executes queued agent runs.
type RunExecutor interface {
	ExecuteRun(ctx context.Context, runID string) error
}

// ReviewService reviews completed agent runs and persists review reports.
type ReviewService interface {
	Review(ctx context.Context, runID string) (*reviewer.ReviewReport, error)
}

// NewRunHandler creates a new run handler.
func NewRunHandler(db *sql.DB, logger *slog.Logger, eventBus *events.Bus) *RunHandler {
	return &RunHandler{db: db, logger: logger, eventBus: eventBus}
}

// WithRunExecutor enables runs.triggered execution dispatch.
func (h *RunHandler) WithRunExecutor(executor RunExecutor) *RunHandler {
	h.executor = executor
	return h
}

// WithReviewer enables completed-run review generation before approval flow.
func (h *RunHandler) WithReviewer(reviewer ReviewService) *RunHandler {
	h.reviewer = reviewer
	return h
}

// HandleRunCompleted processes agents.run.completed events.
// 1. Queue the next role when the run produced a mailbox handoff
// 2. Generate and persist a review report when there is no handoff
func (h *RunHandler) HandleRunCompleted(msg *nats.Msg) error {
	var event events.AgentRunEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal agent run event: %w", err)
	}

	h.logger.Info("handling run completed", "run_id", event.RunID, "task_id", event.TaskID)

	if scheduled, nextRunID, nextRole, err := h.scheduleFollowOnRun(context.Background(), event); err != nil {
		return err
	} else if scheduled {
		h.logger.Info("scheduled follow-on agent run from mailbox handoff",
			"run_id", event.RunID,
			"next_run_id", nextRunID,
			"next_role", nextRole,
		)
		return ackMessage(msg)
	}

	// Update task status to reviewing
	now := time.Now().UTC()
	_, err := h.db.Exec(`
		UPDATE tasks SET status = 'reviewing', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, event.TaskID)
	if err != nil {
		h.logger.Warn("failed to update task status to reviewing", "error", err)
	}

	if h.reviewer == nil {
		return fmt.Errorf("reviewer is not configured")
	}

	report, err := h.reviewer.Review(context.Background(), event.RunID)
	if err != nil {
		return fmt.Errorf("review completed run %s: %w", event.RunID, err)
	}
	if h.eventBus != nil {
		reviewCompletedEvent := map[string]interface{}{
			"run_id":     event.RunID,
			"task_id":    event.TaskID,
			"status":     "completed",
			"risk_level": report.RiskLevel,
			"approvable": report.Approvable,
			"timestamp":  now.Format(time.RFC3339),
		}
		data, _ := json.Marshal(reviewCompletedEvent)
		if pubErr := h.eventBus.Publish("review.completed", data); pubErr != nil {
			return fmt.Errorf("publish review.completed event: %w", pubErr)
		}
	}

	h.logger.Info("run completion processed, review completed",
		"run_id", event.RunID,
		"risk_level", report.RiskLevel,
		"approvable", report.Approvable,
	)
	return ackMessage(msg)
}

type completedRunContext struct {
	RunID       string
	TaskID      string
	WorkspaceID *string
	AgentRole   string
	Model       string
	Provider    string
}

type pendingHandoff struct {
	ID      string
	ToAgent string
}

func (h *RunHandler) scheduleFollowOnRun(ctx context.Context, event events.AgentRunEvent) (bool, string, string, error) {
	if h.db == nil || strings.TrimSpace(event.RunID) == "" {
		return false, "", "", nil
	}
	run, err := h.loadCompletedRunContext(ctx, event)
	if err != nil {
		return false, "", "", err
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return false, "", "", fmt.Errorf("begin follow-on run transaction: %w", err)
	}
	defer tx.Rollback()

	handoff, err := h.nextUnconsumedHandoff(ctx, tx, run)
	if err != nil {
		return false, "", "", err
	}
	if handoff == nil {
		return false, "", "", nil
	}
	if !validAgentRole(handoff.ToAgent) {
		return false, "", "", fmt.Errorf("handoff %s targets unknown agent role %q", handoff.ID, handoff.ToAgent)
	}

	nextRunID := uuid.New().String()
	now := time.Now().UTC()
	metadata, err := json.Marshal(map[string]any{
		"trigger":             "mailbox_handoff",
		"handoff_message_id":  handoff.ID,
		"handoff_from_run_id": run.RunID,
		"handoff_from_agent":  run.AgentRole,
	})
	if err != nil {
		return false, "", "", fmt.Errorf("marshal follow-on run metadata: %w", err)
	}

	workspaceArg := any(nil)
	if run.WorkspaceID != nil {
		workspaceArg = *run.WorkspaceID
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider,
			status, total_cost, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'queued', 0.0, $7, $8, $8)
	`, nextRunID, run.TaskID, workspaceArg, handoff.ToAgent, run.Model, run.Provider, string(metadata), now)
	if err != nil {
		return false, "", "", fmt.Errorf("create follow-on agent run: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_messages
		SET consumed_at = $1, consumed_by_run_id = $2
		WHERE id = $3 AND consumed_at IS NULL
	`, now, nextRunID, handoff.ID)
	if err != nil {
		return false, "", "", fmt.Errorf("mark handoff consumed: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, "", "", fmt.Errorf("check handoff consumption: %w", err)
	}
	if rows == 0 {
		return false, "", "", nil
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE tasks SET status = 'running', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, run.TaskID)
	if err != nil {
		return false, "", "", fmt.Errorf("update task for follow-on run: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, "", "", fmt.Errorf("commit follow-on run: %w", err)
	}

	if h.eventBus != nil {
		payload := map[string]any{
			"run_id":             nextRunID,
			"task_id":            run.TaskID,
			"agent_role":         handoff.ToAgent,
			"status":             "queued",
			"action":             "mailbox_handoff",
			"handoff_message_id": handoff.ID,
			"previous_run_id":    run.RunID,
		}
		data, _ := json.Marshal(payload)
		if err := h.eventBus.Publish(events.RunTriggered, data); err != nil {
			h.logger.Warn("failed to publish follow-on run triggered event", "error", err)
		}
	}

	return true, nextRunID, handoff.ToAgent, nil
}

func (h *RunHandler) loadCompletedRunContext(ctx context.Context, event events.AgentRunEvent) (*completedRunContext, error) {
	var workspaceID, model, provider sql.NullString
	run := &completedRunContext{RunID: event.RunID}
	err := h.db.QueryRowContext(ctx, `
		SELECT task_id, workspace_id, agent_role, model, provider
		FROM agent_runs
		WHERE id = $1
	`, event.RunID).Scan(&run.TaskID, &workspaceID, &run.AgentRole, &model, &provider)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("completed run %s not found", event.RunID)
		}
		return nil, fmt.Errorf("load completed run context: %w", err)
	}
	if event.TaskID != "" && event.TaskID != run.TaskID {
		return nil, fmt.Errorf("completed run %s belongs to task %s, event referenced task %s", event.RunID, run.TaskID, event.TaskID)
	}
	if workspaceID.Valid {
		run.WorkspaceID = &workspaceID.String
	}
	run.Model = "gpt-4o"
	if model.Valid && strings.TrimSpace(model.String) != "" {
		run.Model = model.String
	}
	run.Provider = "openai"
	if provider.Valid && strings.TrimSpace(provider.String) != "" {
		run.Provider = provider.String
	}
	return run, nil
}

func (h *RunHandler) nextUnconsumedHandoff(ctx context.Context, tx *sql.Tx, run *completedRunContext) (*pendingHandoff, error) {
	var handoff pendingHandoff
	err := tx.QueryRowContext(ctx, `
		SELECT id, to_agent
		FROM agent_messages
		WHERE task_id = $1
		  AND message_type = $2
		  AND consumed_at IS NULL
		  AND to_agent <> 'broadcast'
		  AND (agent_run_id = $3 OR agent_run_id IS NULL)
		ORDER BY created_at ASC
		LIMIT 1
	`, run.TaskID, models.MessageTypeHandoff, run.RunID).Scan(&handoff.ID, &handoff.ToAgent)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("load next handoff: %w", err)
	}
	handoff.ToAgent = strings.TrimSpace(handoff.ToAgent)
	return &handoff, nil
}

func validAgentRole(role string) bool {
	switch role {
	case models.AgentRolePlanner,
		models.AgentRoleImplementer,
		models.AgentRoleReviewer,
		models.AgentRoleTestRunner,
		models.AgentRoleSecurity,
		models.AgentRoleDocs,
		models.AgentRoleReleaseManager:
		return true
	default:
		return false
	}
}

// HandleReviewCompleted processes review.completed events.
// 1. Request human approval for PR creation
func (h *RunHandler) HandleReviewCompleted(msg *nats.Msg) error {
	var payload struct {
		RunID     string `json:"run_id"`
		TaskID    string `json:"task_id"`
		Status    string `json:"status"`
		RiskLevel string `json:"risk_level"`
	}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		return fmt.Errorf("unmarshal review completed event: %w", err)
	}

	h.logger.Info("handling review completed", "run_id", payload.RunID, "task_id", payload.TaskID)

	// Check if there's already a pending approval for this task
	var pendingCount int
	err := h.db.QueryRow(`
		SELECT COUNT(*) FROM approvals
		WHERE task_id = $1 AND response IS NULL
		AND (expires_at IS NULL OR expires_at > $2)
	`, payload.TaskID, time.Now().UTC()).Scan(&pendingCount)
	if err != nil {
		h.logger.Warn("failed to check pending approvals", "error", err)
	}
	if pendingCount > 0 {
		h.logger.Info("approval request already pending for task", "task_id", payload.TaskID)
		return ackMessage(msg)
	}

	// Create approval request for PR creation
	approvalID := uuid.New().String()
	now := time.Now().UTC()
	metadata := map[string]interface{}{
		"auto_created": true,
		"reason":       "review_completed",
		"run_id":       payload.RunID,
	}
	metadataJSON, _ := json.Marshal(metadata)

	_, err = h.db.Exec(`
		INSERT INTO approvals (
			id, task_id, agent_run_id, approval_type, requested_by,
			requested_at, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'system', $5, $6, $5, $5)
	`, approvalID, payload.TaskID, payload.RunID, models.ApprovalTypePRCreate, now, metadataJSON)
	if err != nil {
		return fmt.Errorf("create approval request: %w", err)
	}

	h.logger.Info("approval request created for PR creation",
		"approval_id", approvalID,
		"task_id", payload.TaskID,
		"run_id", payload.RunID,
	)

	return ackMessage(msg)
}

// HandleRunTriggered processes runs.triggered events by executing the queued
// agent run through the configured executor.
func (h *RunHandler) HandleRunTriggered(msg *nats.Msg) error {
	var event events.RunEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal run triggered event: %w", err)
	}
	if strings.TrimSpace(event.RunID) == "" {
		return fmt.Errorf("run triggered event missing run_id")
	}
	if h.executor == nil {
		return fmt.Errorf("run executor is not configured")
	}

	h.logger.Info("executing triggered run", "run_id", event.RunID, "task_id", event.TaskID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := h.executor.ExecuteRun(ctx, event.RunID); err != nil {
		return fmt.Errorf("execute run %s: %w", event.RunID, err)
	}
	return ackMessage(msg)
}

func ackMessage(msg *nats.Msg) error {
	if msg == nil || msg.Reply == "" {
		return nil
	}
	if err := msg.Ack(); err != nil && err != nats.ErrMsgNoReply {
		return err
	}
	return nil
}
