package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// AgentRun represents an agent run record.
type AgentRun struct {
	ID               string          `json:"id"`
	TaskID           string          `json:"task_id"`
	WorkspaceID      *string         `json:"workspace_id,omitempty"`
	AgentRole        string          `json:"agent_role"`
	Model            *string         `json:"model,omitempty"`
	Provider         *string         `json:"provider,omitempty"`
	Status           string          `json:"status"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TotalCost        float64         `json:"total_cost"`
	ErrorMessage     *string         `json:"error_message,omitempty"`
	Summary          *string         `json:"summary,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// AgentStep represents an agent step record.
type AgentStep struct {
	ID            string          `json:"id"`
	AgentRunID    string          `json:"agent_run_id"`
	StepNumber    int             `json:"step_number"`
	StepType      string          `json:"step_type"`
	Status        string          `json:"status"`
	Content       *string         `json:"content,omitempty"`
	ToolName      *string         `json:"tool_name,omitempty"`
	ToolInput     json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput    json.RawMessage `json:"tool_output,omitempty"`
	Command       *string         `json:"command,omitempty"`
	CommandOutput *string         `json:"command_output,omitempty"`
	ExitCode      *int            `json:"exit_code,omitempty"`
	FilePath      *string         `json:"file_path,omitempty"`
	Diff          *string         `json:"diff,omitempty"`
	Cost          float64         `json:"cost"`
	LatencyMs     int             `json:"latency_ms"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ListAgentRuns returns all agent runs for a task.
func (h *Handler) ListAgentRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	taskID := chi.URLParam(r, "taskID")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	if err := authz.AuthorizeTask(ctx, h.db, user, taskID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("task not found"))
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, task_id, workspace_id, agent_role, model, provider, status,
		       started_at, completed_at, prompt_tokens, completion_tokens,
		       total_cost, error_message, summary, metadata, created_at, updated_at
		FROM agent_runs
		WHERE task_id = $1
		ORDER BY created_at DESC
	`, taskID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var runs []AgentRun
	for rows.Next() {
		var run AgentRun
		if err := scanAgentRun(rows, &run); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	if runs == nil {
		runs = []AgentRun{}
	}
	respond.JSON(w, http.StatusOK, runs)
}

// GetAgentRun returns a single agent run by ID.
func (h *Handler) GetAgentRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}

	if err := authz.AuthorizeAgentRun(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
		return
	}

	var run AgentRun
	err := scanAgentRun(h.db.QueryRowContext(ctx, `
		SELECT id, task_id, workspace_id, agent_role, model, provider, status,
		       started_at, completed_at, prompt_tokens, completion_tokens,
		       total_cost, error_message, summary, metadata, created_at, updated_at
		FROM agent_runs
		WHERE id = $1
	`, id), &run)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusOK, run)
}

// ListAgentSteps returns all steps for an agent run.
func (h *Handler) ListAgentSteps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	runID := chi.URLParam(r, "id")
	if runID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}

	if err := authz.AuthorizeAgentRun(ctx, h.db, user, runID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, agent_run_id, step_number, step_type, status, content,
		       tool_name, tool_input, tool_output, command, command_output,
		       exit_code, file_path, diff, cost, latency_ms, created_at
		FROM agent_steps
		WHERE agent_run_id = $1
		ORDER BY step_number ASC, created_at ASC
	`, runID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var steps []AgentStep
	for rows.Next() {
		var s AgentStep
		var content, toolName, toolInput, toolOutput, command, commandOutput, filePath, diff sql.NullString
		var exitCode sql.NullInt32
		err := rows.Scan(
			&s.ID, &s.AgentRunID, &s.StepNumber, &s.StepType, &s.Status, &content,
			&toolName, &toolInput, &toolOutput, &command, &commandOutput,
			&exitCode, &filePath, &diff, &s.Cost, &s.LatencyMs, &s.CreatedAt,
		)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if content.Valid {
			s.Content = &content.String
		}
		if toolName.Valid {
			s.ToolName = &toolName.String
		}
		if toolInput.Valid {
			s.ToolInput = json.RawMessage(toolInput.String)
		}
		if toolOutput.Valid {
			s.ToolOutput = json.RawMessage(toolOutput.String)
		}
		if command.Valid {
			s.Command = &command.String
		}
		if commandOutput.Valid {
			s.CommandOutput = &commandOutput.String
		}
		if exitCode.Valid {
			code := int(exitCode.Int32)
			s.ExitCode = &code
		}
		if filePath.Valid {
			s.FilePath = &filePath.String
		}
		if diff.Valid {
			s.Diff = &diff.String
		}
		steps = append(steps, s)
	}

	if steps == nil {
		steps = []AgentStep{}
	}
	respond.JSON(w, http.StatusOK, steps)
}

// CancelAgentRun cancels a running or pending agent run.
func (h *Handler) CancelAgentRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}

	if err := authz.AuthorizeAgentRun(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
		return
	}

	now := time.Now().UTC()
	result, err := h.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = 'cancelled', completed_at = $1, updated_at = $1
		WHERE id = $2 AND status IN ('pending', 'running')
	`, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		respond.Error(w, http.StatusNotFound, errors.New("run not found or already finished"))
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{
		"status": "cancelled",
		"id":     id,
	})
}

// StreamAgentRun streams agent run updates via SSE.
func (h *Handler) StreamAgentRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	runID := chi.URLParam(r, "id")
	if runID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}

	if err := authz.AuthorizeAgentRun(ctx, h.db, user, runID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
		return
	}

	// Create a channel for SSE events
	events := make(chan string, 10)
	defer close(events)

	// Start a goroutine to push updates
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for i := 0; i < 30; i++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Query current run status
				var status string
				err := h.db.QueryRowContext(ctx, `SELECT status FROM agent_runs WHERE id = $1`, runID).Scan(&status)
				if err != nil {
					return
				}
				payload, err := json.Marshal(struct {
					RunID  string `json:"run_id"`
					Status string `json:"status"`
				}{RunID: runID, Status: status})
				if err != nil {
					return
				}
				events <- string(payload)
				if status == "completed" || status == "failed" || status == "cancelled" {
					return
				}
			}
		}
	}()

	respond.SSE(w, r, events)
}

type agentRunScanner interface {
	Scan(dest ...any) error
}

func scanAgentRun(scanner agentRunScanner, run *AgentRun) error {
	var workspaceID, model, provider, errorMessage, summary, metadata sql.NullString
	var startedAt, completedAt sql.NullTime
	if err := scanner.Scan(
		&run.ID, &run.TaskID, &workspaceID, &run.AgentRole, &model, &provider, &run.Status,
		&startedAt, &completedAt, &run.PromptTokens, &run.CompletionTokens,
		&run.TotalCost, &errorMessage, &summary, &metadata, &run.CreatedAt, &run.UpdatedAt,
	); err != nil {
		return err
	}
	if workspaceID.Valid {
		run.WorkspaceID = &workspaceID.String
	}
	if model.Valid {
		run.Model = &model.String
	}
	if provider.Valid {
		run.Provider = &provider.String
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	if errorMessage.Valid {
		run.ErrorMessage = &errorMessage.String
	}
	if summary.Valid {
		run.Summary = &summary.String
	}
	if metadata.Valid {
		run.Metadata = json.RawMessage(metadata.String)
	}
	return nil
}
