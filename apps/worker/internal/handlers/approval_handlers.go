// Package handlers contains event handlers for the worker service.
//
// Approval handlers process approval response events:
//   - approval.approved -> create PR if type=pr_create
//   - approval.rejected -> update task status, notify user
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/prfactory"
)

// WorkerEventPublisher is the subset of the event bus used by approval handlers.
type WorkerEventPublisher interface {
	Publish(subject string, data []byte) error
}

// PullRequestCreator creates pull requests for approved tasks.
type PullRequestCreator interface {
	CreatePullRequest(ctx context.Context, taskID string) (*models.PullRequest, error)
}

// AgentRuntimeApprovalResponder answers a provider-native approval request
// without requeueing the enclosing Dev Plane run.
type AgentRuntimeApprovalResponder interface {
	RespondAgentApproval(ctx context.Context, threadID, turnID, itemID string, approved bool, note string) error
}

// ApprovalHandler handles approval response events.
type ApprovalHandler struct {
	db                        *sql.DB
	logger                    *slog.Logger
	eventBus                  WorkerEventPublisher
	factory                   PullRequestCreator
	agentRuntimeApprovalReply AgentRuntimeApprovalResponder
}

// NewApprovalHandler creates a new approval handler.
func NewApprovalHandler(db *sql.DB, logger *slog.Logger, eventBus *events.Bus) *ApprovalHandler {
	factory := prfactory.NewFactory(db, logger)
	return &ApprovalHandler{
		db:       db,
		logger:   logger,
		eventBus: eventBus,
		factory:  factory,
	}
}

// WithEventPublisher replaces the event publisher, primarily for tests.
func (h *ApprovalHandler) WithEventPublisher(eventBus WorkerEventPublisher) *ApprovalHandler {
	h.eventBus = eventBus
	return h
}

// WithPullRequestCreator replaces the PR creator, primarily for integration tests.
func (h *ApprovalHandler) WithPullRequestCreator(factory PullRequestCreator) *ApprovalHandler {
	h.factory = factory
	return h
}

// WithAgentRuntimeApprovalResponder configures direct provider approval responses.
func (h *ApprovalHandler) WithAgentRuntimeApprovalResponder(responder AgentRuntimeApprovalResponder) *ApprovalHandler {
	h.agentRuntimeApprovalReply = responder
	return h
}

// HandleApprovalApproved processes approval.approved events.
// If approval type is "pr_create", trigger PR creation.
func (h *ApprovalHandler) HandleApprovalApproved(msg *nats.Msg) error {
	var payload struct {
		ApprovalID   string `json:"approval_id"`
		TaskID       string `json:"task_id"`
		AgentRunID   string `json:"agent_run_id"`
		Response     string `json:"response"`
		ResponderID  string `json:"responder_id"`
		ApprovalType string `json:"approval_type"`
		Note         string `json:"note"`
	}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		return fmt.Errorf("unmarshal approval approved event: %w", err)
	}

	h.logger.Info("handling approval approved",
		"approval_id", payload.ApprovalID,
		"task_id", payload.TaskID,
		"agent_run_id", payload.AgentRunID,
		"type", payload.ApprovalType,
	)
	if payload.ApprovalType == models.ApprovalTypeAgentRuntime {
		if err := h.respondAgentRuntimeApproval(context.Background(), payload.ApprovalID, payload.TaskID, payload.AgentRunID, true, payload.Note); err != nil {
			return err
		}
		return ackMessage(msg)
	}
	if isAgentResumeApproval(payload.ApprovalType) {
		if err := h.resumePausedRun(context.Background(), payload.ApprovalID, payload.TaskID, payload.AgentRunID); err != nil {
			return err
		}
		return ackMessage(msg)
	}
	if payload.ApprovalType != models.ApprovalTypePRCreate {
		h.logger.Info("approval is not for PR creation, skipping",
			"approval_id", payload.ApprovalID,
			"type", payload.ApprovalType,
		)
		return ackMessage(msg)
	}

	// Verify the task is in a state that allows PR creation
	var taskStatus string
	err := h.db.QueryRow(`
		SELECT status FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, payload.TaskID).Scan(&taskStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ackMessage(msg)
		}
		return fmt.Errorf("load task status: %w", err)
	}

	// Only create PR if task is in reviewing or pr_created state
	if taskStatus != "reviewing" && taskStatus != "pr_created" {
		h.logger.Info("task not in reviewable state, skipping PR creation",
			"task_id", payload.TaskID,
			"status", taskStatus,
		)
		return ackMessage(msg)
	}

	// Create the pull request
	ctx := context.Background()
	pr, err := h.factory.CreatePullRequest(ctx, payload.TaskID)
	if err != nil {
		h.logger.Error("failed to create pull request",
			"task_id", payload.TaskID,
			"error", err,
		)
		// Don't ack - allow retry
		return fmt.Errorf("create pull request: %w", err)
	}

	h.logger.Info("pull request created after approval",
		"task_id", payload.TaskID,
		"pr_id", pr.ID,
		"pr_number", pr.Number,
	)

	callbackRunID := strings.TrimSpace(payload.AgentRunID)
	if pr.RunID != nil && strings.TrimSpace(*pr.RunID) != "" {
		callbackRunID = strings.TrimSpace(*pr.RunID)
	}
	if err := notifyExternalTaskCallback(ctx, h.db, h.logger, payload.TaskID, externalCallbackOptions{
		EventID:   fmt.Sprintf("dev-plane:task:%s:build.pr_created:%s", payload.TaskID, pr.ID),
		EventType: "build.pr_created",
		RunID:     callbackRunID,
		Status:    "pr_created",
		Artifact: map[string]any{
			"pr_id":     pr.ID,
			"pr_url":    pr.URL,
			"pr_number": pr.Number,
			"branch":    pr.Branch,
		},
	}); err != nil {
		// The PR already exists. Callback delivery must not make PR creation retry
		// and accidentally open a second pull request.
		h.logger.Warn("failed to deliver external PR-created callback", "task_id", payload.TaskID, "error", err)
	}

	return ackMessage(msg)
}

// HandleApprovalRejected processes approval.rejected events.
// Update task status to failed and notify user.
func (h *ApprovalHandler) HandleApprovalRejected(msg *nats.Msg) error {
	var payload struct {
		ApprovalID   string `json:"approval_id"`
		TaskID       string `json:"task_id"`
		AgentRunID   string `json:"agent_run_id"`
		Response     string `json:"response"`
		ResponderID  string `json:"responder_id"`
		ApprovalType string `json:"approval_type"`
		Note         string `json:"note"`
	}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		return fmt.Errorf("unmarshal approval rejected event: %w", err)
	}

	h.logger.Info("handling approval rejected",
		"approval_id", payload.ApprovalID,
		"task_id", payload.TaskID,
		"responder_id", payload.ResponderID,
	)

	if payload.ApprovalType == models.ApprovalTypeAgentRuntime {
		if err := h.respondAgentRuntimeApproval(context.Background(), payload.ApprovalID, payload.TaskID, payload.AgentRunID, false, payload.Note); err != nil {
			return err
		}
		return ackMessage(msg)
	}

	now := time.Now().UTC()

	// Update task status to failed
	_, err := h.db.Exec(`
		UPDATE tasks SET status = 'failed', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, payload.TaskID)
	if err != nil {
		h.logger.Warn("failed to update task status to failed", "error", err)
	}

	// Update the agent run with error message
	runID := strings.TrimSpace(payload.AgentRunID)
	if runID == "" {
		if lookedUpRunID, err := h.loadApprovalRunID(context.Background(), payload.ApprovalID); err == nil {
			runID = lookedUpRunID
		} else {
			h.logger.Warn("failed to load approval run id for rejection", "approval_id", payload.ApprovalID, "error", err)
		}
	}
	failureMessage := fmt.Sprintf("Approval rejected by %s: %s", payload.ResponderID, payload.Note)
	runPredicate := "task_id = $3 AND status IN ('completed', 'reviewed', 'paused')"
	args := []any{failureMessage, now, payload.TaskID}
	if runID != "" {
		runPredicate = "id = $3 AND status IN ('completed', 'reviewed', 'paused')"
		args = []any{failureMessage, now, runID}
	}
	_, err = h.db.Exec(`
		UPDATE agent_runs SET status = 'failed', error_message = $1, updated_at = $2
		WHERE `+runPredicate, args...)
	if err != nil {
		h.logger.Warn("failed to update agent run status", "error", err)
	}

	h.logger.Info("task marked as failed due to approval rejection",
		"task_id", payload.TaskID,
		"approval_id", payload.ApprovalID,
	)

	if err := notifyExternalTaskCallback(context.Background(), h.db, h.logger, payload.TaskID, externalCallbackOptions{
		EventID:   fmt.Sprintf("dev-plane:task:%s:build.failed", payload.TaskID),
		EventType: "build.failed",
		RunID:     runID,
		Status:    "failed",
		Error:     failureMessage,
	}); err != nil {
		h.logger.Warn("failed to deliver external approval-rejected callback", "task_id", payload.TaskID, "error", err)
	}

	return ackMessage(msg)
}

type agentRuntimeApprovalMetadata struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id"`
	ItemID   string `json:"item_id"`
}

func (h *ApprovalHandler) respondAgentRuntimeApproval(ctx context.Context, approvalID, eventTaskID, eventRunID string, approved bool, note string) error {
	if h.agentRuntimeApprovalReply == nil {
		return fmt.Errorf("agent runtime approval responder is not configured")
	}

	var taskID string
	var runID sql.NullString
	var approvalType string
	var metadataRaw []byte
	if err := h.db.QueryRowContext(ctx, `
		SELECT task_id, agent_run_id, approval_type, metadata
		FROM approvals
		WHERE id = $1
	`, approvalID).Scan(&taskID, &runID, &approvalType, &metadataRaw); err != nil {
		return fmt.Errorf("load agent runtime approval %s: %w", approvalID, err)
	}
	if approvalType != models.ApprovalTypeAgentRuntime {
		return fmt.Errorf("approval %s is type %q, want %q", approvalID, approvalType, models.ApprovalTypeAgentRuntime)
	}
	if strings.TrimSpace(eventTaskID) != "" && eventTaskID != taskID {
		return fmt.Errorf("approval %s task mismatch", approvalID)
	}
	resolvedRunID := ""
	if runID.Valid {
		resolvedRunID = strings.TrimSpace(runID.String)
	}
	if strings.TrimSpace(eventRunID) != "" && resolvedRunID != "" && eventRunID != resolvedRunID {
		return fmt.Errorf("approval %s run mismatch", approvalID)
	}

	var metadata agentRuntimeApprovalMetadata
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		return fmt.Errorf("decode agent runtime approval metadata: %w", err)
	}
	if strings.TrimSpace(metadata.ThreadID) == "" || strings.TrimSpace(metadata.TurnID) == "" || strings.TrimSpace(metadata.ItemID) == "" {
		return fmt.Errorf("approval %s is missing runtime thread/turn/item metadata", approvalID)
	}

	if err := h.agentRuntimeApprovalReply.RespondAgentApproval(
		ctx,
		metadata.ThreadID,
		metadata.TurnID,
		metadata.ItemID,
		approved,
		note,
	); err != nil {
		return fmt.Errorf("respond to agent runtime approval %s: %w", approvalID, err)
	}

	now := time.Now().UTC()
	if resolvedRunID != "" {
		if _, err := h.db.ExecContext(ctx, `
			UPDATE agent_runs
			SET status = 'running', error_message = NULL, updated_at = $1
			WHERE id = $2 AND task_id = $3 AND status = 'paused'
		`, now, resolvedRunID, taskID); err != nil {
			return fmt.Errorf("resume direct agent runtime run %s: %w", resolvedRunID, err)
		}
	}
	if _, err := h.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'running', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, taskID); err != nil {
		return fmt.Errorf("resume agent runtime task %s: %w", taskID, err)
	}
	return nil
}

func isAgentResumeApproval(approvalType string) bool {
	approvalType = strings.TrimSpace(approvalType)
	return approvalType == models.ApprovalTypeRiskyAction || strings.HasPrefix(approvalType, "capability:")
}

func (h *ApprovalHandler) resumePausedRun(ctx context.Context, approvalID, taskID, eventRunID string) error {
	runID := strings.TrimSpace(eventRunID)
	if runID == "" {
		var err error
		runID, err = h.loadApprovalRunID(ctx, approvalID)
		if err != nil {
			return fmt.Errorf("load approval run for resume: %w", err)
		}
	}
	if runID == "" {
		return fmt.Errorf("approval %s has no agent run to resume", approvalID)
	}

	now := time.Now().UTC()
	result, err := h.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = 'queued', error_message = NULL, updated_at = $1
		WHERE id = $2 AND task_id = $3 AND status = 'paused'
	`, now, runID, taskID)
	if err != nil {
		return fmt.Errorf("queue paused run %s: %w", runID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check queued paused run %s: %w", runID, err)
	}
	if rows == 0 {
		h.logger.Info("approval did not match a paused run to resume", "approval_id", approvalID, "run_id", runID, "task_id", taskID)
		return nil
	}

	_, err = h.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'running', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, taskID)
	if err != nil {
		return fmt.Errorf("update task for resumed run %s: %w", runID, err)
	}

	if h.eventBus != nil {
		payload := map[string]any{
			"run_id":      runID,
			"task_id":     taskID,
			"status":      models.AgentRunStatusQueued,
			"action":      "approval_resumed",
			"approval_id": approvalID,
		}
		data, _ := json.Marshal(payload)
		if err := h.eventBus.Publish(events.RunTriggered, data); err != nil {
			return fmt.Errorf("publish resumed run triggered event: %w", err)
		}
	}

	h.logger.Info("paused run resumed after approval", "approval_id", approvalID, "run_id", runID, "task_id", taskID)
	return nil
}

func (h *ApprovalHandler) loadApprovalRunID(ctx context.Context, approvalID string) (string, error) {
	if strings.TrimSpace(approvalID) == "" {
		return "", nil
	}
	var runID sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT agent_run_id FROM approvals WHERE id = $1
	`, approvalID).Scan(&runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if !runID.Valid {
		return "", nil
	}
	return strings.TrimSpace(runID.String), nil
}
