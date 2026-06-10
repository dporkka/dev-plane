package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/respond"
	specgenerator "github.com/ai-dev-control-plane/api/internal/spec"
	"github.com/ai-dev-control-plane/events"
)

// GenerateSpec triggers spec generation for a task.
// It transitions the task to "spec_review" and publishes an event for the worker.
func (h *Handler) GenerateSpec(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	// Validate task exists and is in a valid state for spec generation
	var currentStatus string
	err := h.db.QueryRowContext(ctx, `
		SELECT status FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("task not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Spec generation can be triggered from backlog or failed states
	if currentStatus != "backlog" && currentStatus != "failed" {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("cannot generate spec from status: %s", currentStatus))
		return
	}

	generatedSpec, err := specgenerator.NewGenerator(h.db, h.logger).Generate(ctx, taskID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	if h.eventBus != nil {
		data, _ := json.Marshal(events.TaskEvent{
			TaskID: taskID,
			Status: "spec_review",
			Data: mustRawMessage(map[string]string{
				"action":  "spec_generated",
				"spec_id": generatedSpec.ID,
				"agent":   generatedSpec.RecommendedAgent,
			}),
		})
		if pubErr := h.eventBus.Publish(events.TaskUpdated, data); pubErr != nil {
			h.logger.Warn("failed to publish task spec generated event", "task_id", taskID, "spec_id", generatedSpec.ID, "error", pubErr)
		}
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"status":  "spec_review",
		"message": "Spec generated",
		"spec_id": generatedSpec.ID,
	})
}

func mustRawMessage(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// StartRun starts an agent run for an approved task.
// Validates the task is in "approved" status, creates an AgentRun, and publishes an event.
func (h *Handler) StartRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	// Validate task is approved
	var task struct {
		Status       string
		ProjectID    string
		RepositoryID string
		WorkspaceID  *string
		TargetBranch string
	}
	var workspaceID sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT status, project_id, repository_id, workspace_id, target_branch
		FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&task.Status, &task.ProjectID, &task.RepositoryID, &workspaceID, &task.TargetBranch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("task not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	if task.Status != "approved" {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("task must be in 'approved' status, current: %s", task.Status))
		return
	}
	if workspaceID.Valid {
		task.WorkspaceID = &workspaceID.String
	}

	now := time.Now().UTC()
	runID := uuid.New().String()

	// Create the agent run record
	workspaceArg := any(nil)
	if task.WorkspaceID != nil {
		workspaceArg = *task.WorkspaceID
	}
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO agent_runs (id, task_id, workspace_id, agent_role, model, provider, status, total_cost, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, 'implementer', 'gpt-4o', 'openai', 'queued', 0.0, '{}', $4, $4)
	`, runID, taskID, workspaceArg, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Update task status to running
	_, err = h.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'running', started_at = $1, updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, taskID)
	if err != nil {
		h.logger.Warn("failed to update task status to running", "error", err)
	}

	// Publish event to NATS if event bus is available
	if h.eventBus != nil {
		event := map[string]interface{}{
			"run_id":     runID,
			"task_id":    taskID,
			"status":     "queued",
			"action":     "start_run",
			"project_id": task.ProjectID,
		}
		data, _ := json.Marshal(event)
		if pubErr := h.eventBus.Publish("runs.triggered", data); pubErr != nil {
			h.logger.Warn("failed to publish run triggered event", "error", pubErr)
		}
	}

	respond.JSON(w, http.StatusCreated, map[string]interface{}{
		"run_id": runID,
		"status": "queued",
	})
}

// RetryRun retries a failed agent run.
// Gets the original run's config, creates a new run, and publishes an event.
func (h *Handler) RetryRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runID := chi.URLParam(r, "id")
	if runID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}

	// Get the failed run details
	var run struct {
		TaskID      string
		WorkspaceID *string
		AgentRole   string
		Model       *string
		Provider    *string
		Status      string
	}
	var workspaceID, modelValue, providerValue sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT task_id, workspace_id, agent_role, model, provider, status
		FROM agent_runs WHERE id = $1
	`, runID).Scan(&run.TaskID, &workspaceID, &run.AgentRole, &modelValue, &providerValue, &run.Status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	if run.Status != "failed" && run.Status != "cancelled" {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("can only retry failed or cancelled runs, current status: %s", run.Status))
		return
	}
	if workspaceID.Valid {
		run.WorkspaceID = &workspaceID.String
	}
	if modelValue.Valid {
		run.Model = &modelValue.String
	}
	if providerValue.Valid {
		run.Provider = &providerValue.String
	}

	// Get the task to ensure it's in a retryable state
	var taskStatus string
	err = h.db.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id = $1`, run.TaskID).Scan(&taskStatus)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Allow retry from failed, reviewing, or pr_created states
	if taskStatus != "failed" && taskStatus != "reviewing" && taskStatus != "pr_created" {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("task status %s does not allow retry", taskStatus))
		return
	}

	now := time.Now().UTC()
	newRunID := uuid.New().String()

	model := "gpt-4o"
	if run.Model != nil {
		model = *run.Model
	}
	provider := "openai"
	if run.Provider != nil {
		provider = *run.Provider
	}

	// Create new run with same config
	workspaceArg := any(nil)
	if run.WorkspaceID != nil {
		workspaceArg = *run.WorkspaceID
	}
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO agent_runs (id, task_id, workspace_id, agent_role, model, provider, status, total_cost, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'queued', 0.0, '{}', $7, $7)
	`, newRunID, run.TaskID, workspaceArg, run.AgentRole, model, provider, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Update task status to running
	_, err = h.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'running', updated_at = $1 WHERE id = $2
	`, now, run.TaskID)
	if err != nil {
		h.logger.Warn("failed to update task status on retry", "error", err)
	}

	// Publish event to NATS if event bus is available
	if h.eventBus != nil {
		event := map[string]interface{}{
			"run_id":          newRunID,
			"original_run_id": runID,
			"task_id":         run.TaskID,
			"status":          "queued",
			"action":          "retry_run",
		}
		data, _ := json.Marshal(event)
		if pubErr := h.eventBus.Publish("runs.triggered", data); pubErr != nil {
			h.logger.Warn("failed to publish retry run event", "error", pubErr)
		}
	}

	respond.JSON(w, http.StatusCreated, map[string]interface{}{
		"run_id":          newRunID,
		"original_run_id": runID,
		"status":          "queued",
	})
}

// RunEvent represents a high-level event in an agent run's lifecycle.
type RunEvent struct {
	ID         string          `json:"id"`
	AgentRunID string          `json:"agent_run_id"`
	EventType  string          `json:"event_type"`
	Status     string          `json:"status"`
	Message    *string         `json:"message,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// GetRunEvents returns high-level events for a run (like steps but aggregated).
func (h *Handler) GetRunEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runID := chi.URLParam(r, "id")
	if runID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}

	// Verify the run exists
	var exists bool
	err := h.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_runs WHERE id = $1)`, runID).Scan(&exists)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if !exists {
		respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
		return
	}

	// Query agent_steps as high-level events, grouping by step type
	rows, err := h.db.QueryContext(ctx, `
		SELECT id, agent_run_id, step_number, step_type, status, content,
		       tool_name, command, exit_code, cost, created_at
		FROM agent_steps
		WHERE agent_run_id = $1
		ORDER BY step_number ASC, created_at ASC
	`, runID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var events []RunEvent
	for rows.Next() {
		var step struct {
			ID         string
			AgentRunID string
			StepNumber int
			StepType   string
			Status     string
			Content    *string
			ToolName   *string
			Command    *string
			ExitCode   *int
			Cost       float64
			CreatedAt  time.Time
		}
		var content, toolName, command sql.NullString
		var exitCode sql.NullInt32

		err := rows.Scan(
			&step.ID, &step.AgentRunID, &step.StepNumber, &step.StepType, &step.Status,
			&content, &toolName, &command, &exitCode, &step.Cost, &step.CreatedAt,
		)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if content.Valid {
			step.Content = &content.String
		}
		if toolName.Valid {
			step.ToolName = &toolName.String
		}
		if command.Valid {
			step.Command = &command.String
		}
		if exitCode.Valid {
			code := int(exitCode.Int32)
			step.ExitCode = &code
		}

		// Build a human-readable message from the step
		msg := buildEventMessage(step.StepType, step.ToolName, step.Command, step.Status)

		events = append(events, RunEvent{
			ID:         step.ID,
			AgentRunID: step.AgentRunID,
			EventType:  step.StepType,
			Status:     step.Status,
			Message:    &msg,
			CreatedAt:  step.CreatedAt,
		})
	}

	if events == nil {
		events = []RunEvent{}
	}
	respond.JSON(w, http.StatusOK, events)
}

// buildEventMessage creates a human-readable event message from step details.
func buildEventMessage(stepType string, toolName, command *string, status string) string {
	var action string
	switch stepType {
	case "tool_call":
		if toolName != nil {
			action = fmt.Sprintf("Tool call: %s", *toolName)
		} else {
			action = "Tool call"
		}
	case "command":
		if command != nil {
			action = fmt.Sprintf("Command: %s", *command)
		} else {
			action = "Shell command"
		}
	case "file_edit":
		action = "File edit"
	case "thinking":
		action = "Thinking"
	case "error":
		action = "Error"
	case "complete":
		action = "Completed"
	default:
		action = stepType
	}
	return fmt.Sprintf("%s (%s)", action, status)
}
