package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/models"
)
func (r *Runner) loadRunHistory(ctx context.Context, runID string) []models.AgentStep {
	if r.db == nil || strings.TrimSpace(runID) == "" {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, agent_run_id, step_number, step_type, status, outcome, input, output, content,
		       tool_name, tool_input, tool_output, command, command_output,
		       exit_code, file_path, diff, cost, latency_ms, started_at, completed_at, created_at
		FROM agent_steps
		WHERE agent_run_id = $1
		ORDER BY step_number ASC, created_at ASC
	`, runID)
	if err != nil {
		r.logger.Warn("failed to load run history", "run_id", runID, "error", err)
		return nil
	}
	defer rows.Close()

	var history []models.AgentStep
	for rows.Next() {
		var step models.AgentStep
		var outcome, input, output, content, toolName, toolInput, toolOutput, command, commandOutput, filePath, diff sql.NullString
		var exitCode sql.NullInt32
		var startedAt, completedAt sql.NullTime
		if err := rows.Scan(
			&step.ID, &step.AgentRunID, &step.StepNumber, &step.StepType, &step.Status,
			&outcome, &input, &output, &content, &toolName, &toolInput, &toolOutput, &command, &commandOutput,
			&exitCode, &filePath, &diff, &step.Cost, &step.LatencyMs, &startedAt, &completedAt, &step.CreatedAt,
		); err != nil {
			r.logger.Warn("failed to scan run history step", "run_id", runID, "error", err)
			return history
		}
		if outcome.Valid {
			step.Outcome = models.Outcome(outcome.String)
		}
		if input.Valid {
			step.Input = json.RawMessage(input.String)
		}
		if output.Valid {
			step.Output = json.RawMessage(output.String)
		}
		if startedAt.Valid {
			step.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			step.CompletedAt = &completedAt.Time
		}
		if content.Valid {
			step.Content = &content.String
		}
		if toolName.Valid {
			step.ToolName = &toolName.String
		}
		if toolInput.Valid {
			step.ToolInput = json.RawMessage(toolInput.String)
		}
		if toolOutput.Valid {
			step.ToolOutput = json.RawMessage(toolOutput.String)
		}
		if command.Valid {
			step.Command = &command.String
		}
		if commandOutput.Valid {
			step.CommandOutput = &commandOutput.String
		}
		if exitCode.Valid {
			code := int(exitCode.Int32)
			step.ExitCode = &code
		}
		if filePath.Valid {
			step.FilePath = &filePath.String
		}
		if diff.Valid {
			step.Diff = &diff.String
		}
		history = append(history, step)
	}
	if err := rows.Err(); err != nil {
		r.logger.Warn("failed while loading run history", "run_id", runID, "error", err)
	}
	return history
}

func (r *Runner) persistStep(ctx context.Context, step *models.AgentStep) error {
	if r.db == nil {
		return nil
	}

	var input, output, toolInput, toolOutput sql.NullString
	if step.Input != nil {
		input = sql.NullString{String: string(step.Input), Valid: true}
	} else if step.ToolInput != nil {
		input = sql.NullString{String: string(step.ToolInput), Valid: true}
	}
	if step.Output != nil {
		output = sql.NullString{String: string(step.Output), Valid: true}
	} else if step.ToolOutput != nil {
		output = sql.NullString{String: string(step.ToolOutput), Valid: true}
	}
	if step.ToolInput != nil {
		toolInput = sql.NullString{String: string(step.ToolInput), Valid: true}
	}
	if step.ToolOutput != nil {
		toolOutput = sql.NullString{String: string(step.ToolOutput), Valid: true}
	}

	var content, toolName, command, commandOutput, filePath, diff sql.NullString
	if step.Content != nil {
		content = sql.NullString{String: *step.Content, Valid: true}
	}
	if step.ToolName != nil {
		toolName = sql.NullString{String: *step.ToolName, Valid: true}
	}
	if step.Command != nil {
		command = sql.NullString{String: *step.Command, Valid: true}
	}
	if step.CommandOutput != nil {
		commandOutput = sql.NullString{String: *step.CommandOutput, Valid: true}
	}
	if step.FilePath != nil {
		filePath = sql.NullString{String: *step.FilePath, Valid: true}
	}
	if step.Diff != nil {
		diff = sql.NullString{String: *step.Diff, Valid: true}
	}

	now := time.Now().UTC()
	startedAt := step.StartedAt
	if startedAt == nil {
		startedAt = &now
		step.StartedAt = startedAt
	}
	var outcome any
	if step.Outcome.Valid() {
		outcome = string(step.Outcome)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_steps (
			id, agent_run_id, step_number, step_type, status, outcome, input, output, content,
			tool_name, tool_input, tool_output, command, command_output,
			exit_code, file_path, diff, cost, latency_ms, started_at, completed_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
	`, step.ID, step.AgentRunID, step.StepNumber, step.StepType, step.Status, outcome, input, output, content,
		toolName, toolInput, toolOutput, command, commandOutput,
		nil, filePath, diff, step.Cost, step.LatencyMs, startedAt, step.CompletedAt, now,
	)
	return err
}

func (r *Runner) updateStepStatus(ctx context.Context, step *models.AgentStep) error {
	if r.db == nil {
		return nil
	}

	var output, toolOutput sql.NullString
	if step.Output != nil {
		output = sql.NullString{String: string(step.Output), Valid: true}
	} else if step.ToolOutput != nil {
		output = sql.NullString{String: string(step.ToolOutput), Valid: true}
	}
	if step.ToolOutput != nil {
		toolOutput = sql.NullString{String: string(step.ToolOutput), Valid: true}
	}

	var exitCode sql.NullInt32
	if step.ExitCode != nil {
		exitCode = sql.NullInt32{Int32: int32(*step.ExitCode), Valid: true}
	}

	var completedAt any
	if step.Status == models.AgentStepStatusCompleted || step.Status == models.AgentStepStatusFailed {
		now := time.Now().UTC()
		step.CompletedAt = &now
		completedAt = now
		if !step.Outcome.Valid() {
			if step.Status == models.AgentStepStatusCompleted {
				step.Outcome = models.OutcomePassed
			} else {
				step.Outcome = models.OutcomeError
			}
		}
	}
	var outcome any
	if step.Outcome.Valid() {
		outcome = string(step.Outcome)
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_steps
		SET status = $1, outcome = $2, output = $3, tool_output = $4, exit_code = $5,
		    latency_ms = $6, content = $7, completed_at = COALESCE($8, completed_at)
		WHERE id = $9
	`, step.Status, outcome, output, toolOutput, exitCode, step.LatencyMs, step.Content, completedAt, step.ID)
	return err
}

func (r *Runner) loadAgentRun(ctx context.Context, runID string) (*models.AgentRun, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var run models.AgentRun
	var parentRunID, workspaceID, model, provider, outcome, executionSnapshot, executionSnapshotDigest, errorMessage, summary sql.NullString
	var startedAt, completedAt, createdAt, updatedAt sql.NullTime
	var metadata sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT id, task_id, parent_run_id, workspace_id, attempt, agent_role, model, provider, status,
		       outcome, execution_snapshot, execution_snapshot_digest,
		       started_at, completed_at, prompt_tokens, completion_tokens,
		       total_cost, error_message, summary, metadata, created_at, updated_at
		FROM agent_runs WHERE id = $1
	`, runID).Scan(
		&run.ID, &run.TaskID, &parentRunID, &workspaceID, &run.Attempt, &run.AgentRole, &model, &provider, &run.Status,
		&outcome, &executionSnapshot, &executionSnapshotDigest,
		&startedAt, &completedAt, &run.PromptTokens, &run.CompletionTokens,
		&run.TotalCost, &errorMessage, &summary, &metadata, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	if parentRunID.Valid {
		run.ParentRunID = &parentRunID.String
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
	if outcome.Valid {
		run.Outcome = models.Outcome(outcome.String)
	}
	if executionSnapshot.Valid {
		if err := json.Unmarshal([]byte(executionSnapshot.String), &run.ExecutionSnapshot); err != nil {
			return nil, fmt.Errorf("decode execution snapshot: %w", err)
		}
	}
	if executionSnapshotDigest.Valid {
		run.ExecutionSnapshotDigest = executionSnapshotDigest.String
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
	if createdAt.Valid {
		run.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		run.UpdatedAt = updatedAt.Time
	}

	return &run, nil
}

func (r *Runner) loadTask(ctx context.Context, taskID string) (*models.Task, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	var task models.Task
	var workspaceID, description, sourceID sql.NullString
	var spec, acceptanceCriteria, approvalReqs, metadata sql.NullString
	var maxCost sql.NullFloat64
	var maxRuntime sql.NullInt32
	var startedAt, completedAt, deletedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, `
		SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id,
		       title, description, status, priority, risk_level, target_branch,
		       spec, acceptance_criteria, max_cost, max_runtime_minutes,
		       approval_requirements, metadata, started_at, completed_at,
		       created_at, updated_at, deleted_at
		FROM tasks WHERE id = $1
	`, taskID).Scan(
		&task.ID, &task.ProjectID, &task.RepositoryID, &workspaceID, &task.CreatedBy, &task.Source, &sourceID,
		&task.Title, &description, &task.Status, &task.Priority, &task.RiskLevel, &task.TargetBranch,
		&spec, &acceptanceCriteria, &maxCost, &maxRuntime,
		&approvalReqs, &metadata, &startedAt, &completedAt,
		&task.CreatedAt, &task.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}

	if workspaceID.Valid {
		task.WorkspaceID = &workspaceID.String
	}
	if description.Valid {
		task.Description = &description.String
	}
	if sourceID.Valid {
		task.SourceID = &sourceID.String
	}
	if spec.Valid {
		task.Spec = json.RawMessage(spec.String)
	}
	if acceptanceCriteria.Valid {
		task.AcceptanceCriteria = json.RawMessage(acceptanceCriteria.String)
	}
	if maxCost.Valid {
		task.MaxCost = &maxCost.Float64
	}
	if maxRuntime.Valid {
		task.MaxRuntimeMinutes = int(maxRuntime.Int32)
	}
	if approvalReqs.Valid {
		task.ApprovalRequirements = json.RawMessage(approvalReqs.String)
	}
	if metadata.Valid {
		task.Metadata = json.RawMessage(metadata.String)
	}
	if startedAt.Valid {
		task.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}
	if deletedAt.Valid {
		task.DeletedAt = &deletedAt.Time
	}

	return &task, nil
}

func (r *Runner) loadWorkspace(ctx context.Context, workspaceID *string) (*models.Workspace, error) {
	if r.db == nil || workspaceID == nil {
		return nil, fmt.Errorf("workspace ID is nil")
	}

	var ws models.Workspace
	var taskID, worktreePath, runtimeSessionID, previewURL, settings sql.NullString
	var deletedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, `
		SELECT id, repository_id, task_id, name, branch, base_branch, worktree_path,
		       runtime_provider, runtime_session_id, status, preview_url,
		       settings, created_at, updated_at, deleted_at
		FROM workspaces WHERE id = $1
	`, *workspaceID).Scan(
		&ws.ID, &ws.RepositoryID, &taskID, &ws.Name, &ws.Branch, &ws.BaseBranch, &worktreePath,
		&ws.RuntimeProvider, &runtimeSessionID, &ws.Status, &previewURL,
		&settings, &ws.CreatedAt, &ws.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}

	if taskID.Valid {
		ws.TaskID = &taskID.String
	}
	if worktreePath.Valid {
		ws.WorktreePath = &worktreePath.String
	}
	if runtimeSessionID.Valid {
		ws.RuntimeSessionID = &runtimeSessionID.String
	}
	if previewURL.Valid {
		ws.PreviewURL = &previewURL.String
	}
	if settings.Valid {
		ws.Settings = json.RawMessage(settings.String)
	}
	if deletedAt.Valid {
		ws.DeletedAt = &deletedAt.Time
	}

	return &ws, nil
}

func (r *Runner) loadMailboxMessages(ctx context.Context, taskID, agentRole string, limit int) []models.AgentMessage {
	if r.db == nil || taskID == "" {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, task_id, agent_run_id, from_agent, to_agent, message_type, content, metadata, created_at
		FROM agent_messages
		WHERE task_id = $1 AND (to_agent = $2 OR to_agent = 'broadcast')
		ORDER BY created_at ASC
		LIMIT $3
	`, taskID, agentRole, limit)
	if err != nil {
		r.logger.Warn("failed to load mailbox messages", "task_id", taskID, "agent_role", agentRole, "error", err)
		return nil
	}
	defer rows.Close()

	var messages []models.AgentMessage
	for rows.Next() {
		var id, tid, runID, from, to, messageType, content, metadata sql.NullString
		var createdAt sql.NullTime
		if err := rows.Scan(&id, &tid, &runID, &from, &to, &messageType, &content, &metadata, &createdAt); err != nil {
			r.logger.Warn("failed to scan mailbox message", "task_id", taskID, "error", err)
			continue
		}
		msg := models.NullAgentMessage(id, tid, runID, from, to, messageType, content, metadata, createdAt)
		if msg != nil {
			messages = append(messages, *msg)
		}
	}
	if err := rows.Err(); err != nil {
		r.logger.Warn("failed to iterate mailbox messages", "task_id", taskID, "error", err)
	}
	return messages
}

func (r *Runner) persistAgentMessage(ctx context.Context, taskID, runID, fromAgent, toAgent, messageType, content string, metadata map[string]any) error {
	if r.db == nil {
		return nil
	}
	if taskID == "" || fromAgent == "" || toAgent == "" || messageType == "" || strings.TrimSpace(content) == "" {
		return fmt.Errorf("agent message missing required fields")
	}
	metadataJSON := "{}"
	if metadata != nil {
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal message metadata: %w", err)
		}
		metadataJSON = string(encoded)
	}
	var runIDValue any
	if strings.TrimSpace(runID) != "" {
		runIDValue = runID
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_messages (
			id, task_id, agent_run_id, from_agent, to_agent, message_type, content, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, uuid.New().String(), taskID, runIDValue, fromAgent, toAgent, messageType, content, metadataJSON, time.Now().UTC())
	return err
}

func (r *Runner) publishEvent(ctx context.Context, stream, subject string, payload map[string]any) error {
	if r.eventBus == nil {
		return nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return r.eventBus.Publish(subject, data)
}