// Package handlers contains event handlers for the worker service.
//
// Run handlers process agent run lifecycle events:
//   - agents.run.completed -> consume mailbox handoffs or review completed work
//   - agents.run.failed -> transition task to failed and notify integrations
//   - review.completed -> request human approval for PR creation
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

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/reviewer"
)

// RunHandler handles agent run lifecycle events.
type RunHandler struct {
	db          *sql.DB
	logger      *slog.Logger
	eventBus    WorkerEventPublisher
	executor    RunExecutor
	reviewer    ReviewService
	admission   RunAdmission
	repairLimit int
}

// RunExecutor executes queued agent runs.
type RunExecutor interface {
	ExecuteRun(ctx context.Context, runID string) error
}

var ErrRunAdmissionDeferred = errors.New("run admission deferred")

type RunAdmissionDecision struct {
	Allowed    bool
	Reason     string
	RetryAfter time.Duration
}

type RunAdmission interface {
	AdmitRun(ctx context.Context, runID, taskID string) (RunAdmissionDecision, error)
	ReleaseRun(ctx context.Context, runID string) error
}

// ReviewService reviews completed agent runs and persists review reports.
type ReviewService interface {
	Review(ctx context.Context, runID string) (*reviewer.ReviewReport, error)
}

// NewRunHandler creates a new run handler.
func NewRunHandler(db *sql.DB, logger *slog.Logger, eventBus WorkerEventPublisher) *RunHandler {
	return &RunHandler{db: db, logger: logger, eventBus: eventBus, repairLimit: 2}
}

func (h *RunHandler) WithRepairLimit(limit int) *RunHandler {
	if limit < 0 {
		limit = 0
	}
	h.repairLimit = limit
	return h
}

// WithRunExecutor enables runs.triggered execution dispatch.
func (h *RunHandler) WithRunExecutor(executor RunExecutor) *RunHandler {
	h.executor = executor
	return h
}

func (h *RunHandler) WithRunAdmission(admission RunAdmission) *RunHandler {
	h.admission = admission
	return h
}

// WithReviewer enables completed-run review generation before approval flow.
func (h *RunHandler) WithReviewer(reviewer ReviewService) *RunHandler {
	h.reviewer = reviewer
	return h
}

// WithEventPublisher replaces the event publisher, primarily for tests.
func (h *RunHandler) WithEventPublisher(eventBus WorkerEventPublisher) *RunHandler {
	h.eventBus = eventBus
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

	h.logger.Info("handling run completed", "run_id", event.RunID, "task_id", event.TaskID, "state_version", event.StateVersion)

	claim, err := dbpkg.ClaimAgentRunLifecycleEvent(context.Background(), h.db, dbpkg.AgentRunLifecycleEvent{
		RunID:                event.RunID,
		Status:               models.AgentRunStatusCompleted,
		StateVersion:         event.StateVersion,
		RequireLatestAttempt: true,
	})
	if errors.Is(err, dbpkg.ErrRunNotFound) {
		return ackMessage(msg)
	}
	if err != nil {
		return fmt.Errorf("claim completed run event: %w", err)
	}
	if !claim.Claimed {
		h.logger.Info("ignoring duplicate, stale, or superseded run completion", "run_id", event.RunID, "state_version", event.StateVersion)
		return ackMessage(msg)
	}
	claimCompleted := false
	defer func() {
		if !claimCompleted {
			if releaseErr := dbpkg.ReleaseAgentRunLifecycleEvent(context.Background(), h.db, claim); releaseErr != nil {
				h.logger.Warn("failed to release run completion claim", "run_id", event.RunID, "error", releaseErr)
			}
		}
	}()

	if scheduled, nextRunID, nextRole, err := h.scheduleFollowOnRun(context.Background(), event); err != nil {
		return err
	} else if scheduled {
		h.logger.Info("scheduled follow-on agent run from mailbox handoff",
			"run_id", event.RunID,
			"next_run_id", nextRunID,
			"next_role", nextRole,
		)
		if err := dbpkg.CompleteAgentRunLifecycleEvent(context.Background(), h.db, claim); err != nil {
			return fmt.Errorf("complete run lifecycle claim: %w", err)
		}
		claimCompleted = true
		return ackMessage(msg)
	}

	// Update task status to reviewing
	now := time.Now().UTC()
	_, err = h.db.Exec(`
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
	if !report.Approvable {
		if err := h.handleRejectedReview(context.Background(), event, report); err != nil {
			return err
		}
		if err := dbpkg.CompleteAgentRunLifecycleEvent(context.Background(), h.db, claim); err != nil {
			return fmt.Errorf("complete rejected-review lifecycle claim: %w", err)
		}
		claimCompleted = true
		return ackMessage(msg)
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
	if err := dbpkg.CompleteAgentRunLifecycleEvent(context.Background(), h.db, claim); err != nil {
		return fmt.Errorf("complete run lifecycle claim: %w", err)
	}
	claimCompleted = true
	return ackMessage(msg)
}

// HandleRunFailed processes agents.run.failed events.
// It transitions the associated task to failed and publishes a tasks.failed event.
func (h *RunHandler) HandleRunFailed(msg *nats.Msg) error {
	var event events.AgentRunEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal agent run event: %w", err)
	}

	h.logger.Info("handling run failed", "run_id", event.RunID, "task_id", event.TaskID, "state_version", event.StateVersion)

	claim, err := dbpkg.ClaimAgentRunLifecycleEvent(context.Background(), h.db, dbpkg.AgentRunLifecycleEvent{
		RunID:                event.RunID,
		Status:               models.AgentRunStatusFailed,
		StateVersion:         event.StateVersion,
		RequireLatestAttempt: true,
	})
	if errors.Is(err, dbpkg.ErrRunNotFound) {
		return ackMessage(msg)
	}
	if err != nil {
		return fmt.Errorf("claim failed run event: %w", err)
	}
	if !claim.Claimed {
		h.logger.Info("ignoring duplicate, stale, or superseded run failure", "run_id", event.RunID, "state_version", event.StateVersion)
		return ackMessage(msg)
	}
	claimCompleted := false
	defer func() {
		if !claimCompleted {
			if releaseErr := dbpkg.ReleaseAgentRunLifecycleEvent(context.Background(), h.db, claim); releaseErr != nil {
				h.logger.Warn("failed to release run failure claim", "run_id", event.RunID, "error", releaseErr)
			}
		}
	}()

	now := time.Now().UTC()
	_, err = h.db.Exec(`
		UPDATE tasks SET status = 'failed', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, event.TaskID)
	if err != nil {
		return fmt.Errorf("update task status to failed: %w", err)
	}

	if h.eventBus != nil {
		failedEvent := events.TaskEvent{
			TaskID: event.TaskID,
			Status: string(models.TaskStatusFailed),
			Data:   event.Data,
		}
		data, _ := json.Marshal(failedEvent)
		if pubErr := h.eventBus.Publish(events.TaskFailed, data); pubErr != nil {
			return fmt.Errorf("publish tasks.failed event: %w", pubErr)
		}
	}

	h.logger.Info("run failure processed, task transitioned to failed",
		"run_id", event.RunID,
		"task_id", event.TaskID,
	)
	if err := dbpkg.CompleteAgentRunLifecycleEvent(context.Background(), h.db, claim); err != nil {
		return fmt.Errorf("complete failed-run lifecycle claim: %w", err)
	}
	claimCompleted = true
	return ackMessage(msg)
}

type completedRunContext struct {
	RunID             string
	TaskID            string
	WorkspaceID       *string
	AgentRole         string
	Model             string
	Provider          string
	Attempt           int
	ExecutionSnapshot models.ExecutionSnapshot
	Metadata          json.RawMessage
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
	snapshot := run.ExecutionSnapshot
	if snapshot.RecipeVersion == "" {
		snapshot.RecipeVersion = "agent-run/v1"
	}
	snapshot.AgentProfileVersion = handoff.ToAgent + "/v1"
	snapshot.ModelRoute = run.Provider + "/" + run.Model
	if snapshot.VerificationProfile == "" {
		snapshot.VerificationProfile = "project/default"
	}
	snapshotDigest, err := snapshot.Digest()
	if err != nil {
		return false, "", "", fmt.Errorf("digest follow-on execution snapshot: %w", err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return false, "", "", fmt.Errorf("marshal follow-on execution snapshot: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_runs (
			id, task_id, parent_run_id, workspace_id, attempt, agent_role, model, provider,
			status, execution_snapshot, execution_snapshot_digest, total_cost, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'queued', $9, $10, 0.0, $11, $12, $12)
	`, nextRunID, run.TaskID, run.RunID, workspaceArg, run.Attempt+1, handoff.ToAgent, run.Model, run.Provider,
		string(snapshotJSON), snapshotDigest, string(metadata), now)
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
			"parent_run_id":      run.RunID,
			"attempt":            run.Attempt + 1,
		}
		data, _ := json.Marshal(payload)
		if err := h.eventBus.Publish(events.RunTriggered, data); err != nil {
			h.logger.Warn("failed to publish follow-on run triggered event", "error", err)
		}
	}

	return true, nextRunID, handoff.ToAgent, nil
}

func (h *RunHandler) loadCompletedRunContext(ctx context.Context, event events.AgentRunEvent) (*completedRunContext, error) {
	var workspaceID, model, provider, executionSnapshot, metadata sql.NullString
	run := &completedRunContext{RunID: event.RunID}
	err := h.db.QueryRowContext(ctx, `
		SELECT task_id, workspace_id, agent_role, model, provider, attempt, execution_snapshot, metadata
		FROM agent_runs
		WHERE id = $1
	`, event.RunID).Scan(
		&run.TaskID, &workspaceID, &run.AgentRole, &model, &provider, &run.Attempt, &executionSnapshot, &metadata,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
	if run.Attempt < 1 {
		run.Attempt = 1
	}
	if executionSnapshot.Valid && strings.TrimSpace(executionSnapshot.String) != "" {
		if err := json.Unmarshal([]byte(executionSnapshot.String), &run.ExecutionSnapshot); err != nil {
			return nil, fmt.Errorf("decode execution snapshot: %w", err)
		}
	}
	if metadata.Valid {
		run.Metadata = json.RawMessage(metadata.String)
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
		if errors.Is(err, sql.ErrNoRows) {
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

func repairRound(metadata json.RawMessage) int {
	if len(metadata) == 0 {
		return 0
	}
	var values map[string]any
	if err := json.Unmarshal(metadata, &values); err != nil {
		return 0
	}
	switch value := values["repair_round"].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

func reviewFeedback(report *reviewer.ReviewReport) string {
	if report == nil {
		return "Review rejected the candidate. Inspect the existing changes, repair the issues, and rerun verification."
	}
	var b strings.Builder
	if strings.TrimSpace(report.Summary) != "" {
		b.WriteString(strings.TrimSpace(report.Summary))
		b.WriteString("\n")
	}
	for _, finding := range report.Findings {
		fmt.Fprintf(&b, "- [%s] %s", finding.Severity, finding.Message)
		if finding.File != "" {
			fmt.Fprintf(&b, " (%s", finding.File)
			if finding.Line > 0 {
				fmt.Fprintf(&b, ":%d", finding.Line)
			}
			b.WriteString(")")
		}
		if finding.Suggestion != "" {
			fmt.Fprintf(&b, " — %s", finding.Suggestion)
		}
		b.WriteString("\n")
	}
	if len(report.Suggestions) > 0 {
		b.WriteString("Suggested repairs:\n")
		for _, suggestion := range report.Suggestions {
			if strings.TrimSpace(suggestion) != "" {
				fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(suggestion))
			}
		}
	}
	if b.Len() == 0 {
		b.WriteString("Review rejected the candidate. Inspect the existing changes, repair the issues, and rerun verification.")
	}
	return strings.TrimSpace(b.String())
}

func (h *RunHandler) handleRejectedReview(ctx context.Context, event events.AgentRunEvent, report *reviewer.ReviewReport) error {
	run, err := h.loadCompletedRunContext(ctx, event)
	if err != nil {
		return err
	}
	currentRound := repairRound(run.Metadata)
	now := time.Now().UTC()

	if currentRound < h.repairLimit {
		var existingChild string
		err := h.db.QueryRowContext(ctx, `
			SELECT id
			FROM agent_runs
			WHERE parent_run_id = $1
			  AND attempt = $2
			  AND agent_role = $3
			ORDER BY created_at ASC
			LIMIT 1
		`, run.RunID, run.Attempt+1, models.AgentRoleImplementer).Scan(&existingChild)
		if err == nil {
			h.logger.Info("review repair already scheduled", "run_id", run.RunID, "repair_run_id", existingChild)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check existing review repair: %w", err)
		}
	}

	if _, err := h.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET outcome = $1, updated_at = $2
		WHERE id = $3
	`, models.OutcomeFailed, now, run.RunID); err != nil {
		return fmt.Errorf("mark reviewed run failed: %w", err)
	}

	feedback := reviewFeedback(report)
	if currentRound >= h.repairLimit {
		if _, err := h.db.ExecContext(ctx, `
			UPDATE tasks SET status = $1, updated_at = $2
			WHERE id = $3 AND deleted_at IS NULL
		`, models.TaskStatusFailed, now, run.TaskID); err != nil {
			return fmt.Errorf("fail task after repair budget exhausted: %w", err)
		}
		metadata, _ := json.Marshal(map[string]any{
			"reason":        "repair_budget_exhausted",
			"repair_round":  currentRound,
			"repair_limit":  h.repairLimit,
			"review_run_id": run.RunID,
		})
		_, _ = h.db.ExecContext(ctx, `
			INSERT INTO agent_messages (
				id, task_id, agent_run_id, from_agent, to_agent, message_type, content, metadata, created_at
			) VALUES ($1, $2, $3, $4, 'human', $5, $6, $7, $8)
		`, uuid.New().String(), run.TaskID, run.RunID, models.AgentRoleReviewer, models.MessageTypeEscalation, feedback, string(metadata), now)
		if h.eventBus != nil {
			payload := events.TaskEvent{
				TaskID: run.TaskID,
				Status: string(models.TaskStatusFailed),
				Data:   json.RawMessage(metadata),
			}
			data, _ := json.Marshal(payload)
			if err := h.eventBus.Publish(events.TaskFailed, data); err != nil {
				return fmt.Errorf("publish repair budget exhausted: %w", err)
			}
		}
		return nil
	}

	nextRound := currentRound + 1
	nextRunID := uuid.New().String()
	feedbackMetadata, _ := json.Marshal(map[string]any{
		"trigger":       "review_repair",
		"repair_round":  nextRound,
		"review_run_id": run.RunID,
		"risk_level":    report.RiskLevel,
	})
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO agent_messages (
			id, task_id, agent_run_id, from_agent, to_agent, message_type, content, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, uuid.New().String(), run.TaskID, run.RunID, models.AgentRoleReviewer, models.AgentRoleImplementer,
		models.MessageTypeReview, feedback, string(feedbackMetadata), now); err != nil {
		return fmt.Errorf("persist review repair feedback: %w", err)
	}

	snapshot := run.ExecutionSnapshot
	if snapshot.RecipeVersion == "" {
		snapshot.RecipeVersion = "agent-run/v1"
	}
	snapshot.AgentProfileVersion = models.AgentRoleImplementer + "/v1"
	snapshot.ModelRoute = run.Provider + "/" + run.Model
	if snapshot.VerificationProfile == "" {
		snapshot.VerificationProfile = "project/default"
	}
	snapshotDigest, err := snapshot.Digest()
	if err != nil {
		return fmt.Errorf("digest repair execution snapshot: %w", err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal repair execution snapshot: %w", err)
	}
	runMetadata, _ := json.Marshal(map[string]any{
		"trigger":       "review_repair",
		"repair_round":  nextRound,
		"review_run_id": run.RunID,
		"review_risk":   report.RiskLevel,
	})
	var workspace any
	if run.WorkspaceID != nil {
		workspace = *run.WorkspaceID
	}
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO agent_runs (
			id, task_id, parent_run_id, workspace_id, attempt, agent_role, model, provider,
			status, execution_snapshot, execution_snapshot_digest, total_cost, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0.0, $12, $13, $13)
	`, nextRunID, run.TaskID, run.RunID, workspace, run.Attempt+1, models.AgentRoleImplementer,
		run.Model, run.Provider, models.AgentRunStatusQueued, string(snapshotJSON), snapshotDigest, string(runMetadata), now); err != nil {
		return fmt.Errorf("create review repair run: %w", err)
	}
	if _, err := h.db.ExecContext(ctx, `
		UPDATE tasks SET status = $1, updated_at = $2
		WHERE id = $3 AND deleted_at IS NULL
	`, models.TaskStatusRunning, now, run.TaskID); err != nil {
		return fmt.Errorf("queue task for review repair: %w", err)
	}
	if h.eventBus != nil {
		payload := map[string]any{
			"run_id":        nextRunID,
			"task_id":       run.TaskID,
			"parent_run_id": run.RunID,
			"attempt":       run.Attempt + 1,
			"repair_round":  nextRound,
			"status":        models.AgentRunStatusQueued,
			"action":        "review_repair",
		}
		data, _ := json.Marshal(payload)
		if err := h.eventBus.Publish(events.RunTriggered, data); err != nil {
			return fmt.Errorf("publish review repair run: %w", err)
		}
	}
	return nil
}

// HandleReviewCompleted processes review.completed events.
// 1. Request human approval for PR creation
func (h *RunHandler) HandleReviewCompleted(msg *nats.Msg) error {
	var payload struct {
		RunID      string `json:"run_id"`
		TaskID     string `json:"task_id"`
		Status     string `json:"status"`
		RiskLevel  string `json:"risk_level"`
		Approvable *bool  `json:"approvable,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		return fmt.Errorf("unmarshal review completed event: %w", err)
	}

	h.logger.Info("handling review completed", "run_id", payload.RunID, "task_id", payload.TaskID)
	if payload.Approvable != nil && !*payload.Approvable {
		h.logger.Info("review is explicitly non-approvable; skipping PR approval", "run_id", payload.RunID, "task_id", payload.TaskID)
		return ackMessage(msg)
	}

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
	if h.admission != nil {
		decision, err := h.admission.AdmitRun(ctx, event.RunID, event.TaskID)
		if err != nil {
			return fmt.Errorf("admit run %s: %w", event.RunID, err)
		}
		if !decision.Allowed {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "scheduler admission denied"
			}
			retryAfter := decision.RetryAfter
			if retryAfter <= 0 {
				retryAfter = 5 * time.Second
			}
			if msg != nil && msg.Reply != "" {
				if nakErr := msg.NakWithDelay(retryAfter); nakErr != nil &&
					!errors.Is(nakErr, nats.ErrMsgNotBound) &&
					!errors.Is(nakErr, nats.ErrMsgNoReply) {
					return fmt.Errorf("defer run admission: %w", nakErr)
				}
			}
			return fmt.Errorf("%w: run %s not admitted: %s", ErrRunAdmissionDeferred, event.RunID, reason)
		}
	}
	err := h.executor.ExecuteRun(ctx, event.RunID)
	if h.admission != nil {
		if releaseErr := h.admission.ReleaseRun(ctx, event.RunID); releaseErr != nil {
			if err == nil {
				return fmt.Errorf("release run admission %s: %w", event.RunID, releaseErr)
			}
			h.logger.Warn("failed to release run admission after execution error", "run_id", event.RunID, "error", releaseErr)
		}
	}
	if err != nil {
		if errors.Is(err, dbpkg.ErrInvalidTransition) ||
			errors.Is(err, dbpkg.ErrStaleRunState) ||
			errors.Is(err, dbpkg.ErrRunNotFound) {
			h.logger.Info("ignoring stale run trigger", "run_id", event.RunID, "error", err)
			return ackMessage(msg)
		}
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
