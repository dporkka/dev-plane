package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/budget"
	"github.com/ai-dev-control-plane/api/internal/modelrouter"
	"github.com/ai-dev-control-plane/models"
)
func (r *Runner) checkBudget(ctx context.Context, taskID string, state *RunState) (*budget.CheckResult, error) {
	if r.budget == nil {
		return &budget.CheckResult{Allowed: true}, nil
	}

	// Load task budget if available
	taskBudget, _ := r.loadTaskBudget(ctx, taskID)
	if taskBudget == nil {
		return &budget.CheckResult{Allowed: true}, nil
	}

	rs := &budget.RunState{
		CostSoFar:       state.CostSoFar,
		DurationMinutes: state.DurationMinutes,
		ModelCalls:      state.ModelCalls,
		ToolCalls:       state.ToolCalls,
		ShellCommands:   state.ShellCommands,
		FilesChanged:    state.FilesChanged,
	}

	return r.budget.CheckRun(ctx, taskBudget, rs)
}

func (r *Runner) loadTaskBudget(ctx context.Context, taskID string) (*models.Budget, error) {
	if r.db == nil {
		return nil, nil
	}

	var b models.Budget
	var projectID, taskIDField sql.NullString
	var maxCost, maxDailySpend sql.NullFloat64
	var maxRuntime, maxModelCalls, maxToolCalls, maxShellCommands, maxConcurrent sql.NullInt32
	var notifications sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT id, organization_id, project_id, task_id, type, period,
		       max_cost, max_runtime_minutes, max_model_calls, max_tool_calls,
		       max_shell_commands, max_concurrent_agents, max_daily_spend,
		       notifications, created_at, updated_at
		FROM budgets WHERE task_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, taskID).Scan(
		&b.ID, &b.OrganizationID, &projectID, &taskIDField, &b.Type, &b.Period,
		&maxCost, &maxRuntime, &maxModelCalls, &maxToolCalls,
		&maxShellCommands, &maxConcurrent, &maxDailySpend,
		&notifications, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		// Also try project-level budget
		return r.loadProjectBudgetForTask(ctx, taskID)
	}

	if projectID.Valid {
		b.ProjectID = &projectID.String
	}
	if taskIDField.Valid {
		b.TaskID = &taskIDField.String
	}
	if maxCost.Valid {
		b.MaxCost = &maxCost.Float64
	}
	if maxRuntime.Valid {
		b.MaxRuntimeMinutes = int(maxRuntime.Int32)
	}
	if maxModelCalls.Valid {
		b.MaxModelCalls = int(maxModelCalls.Int32)
	}
	if maxToolCalls.Valid {
		b.MaxToolCalls = int(maxToolCalls.Int32)
	}
	if maxShellCommands.Valid {
		b.MaxShellCommands = int(maxShellCommands.Int32)
	}
	if maxConcurrent.Valid {
		b.MaxConcurrentAgents = int(maxConcurrent.Int32)
	}
	if maxDailySpend.Valid {
		b.MaxDailySpend = &maxDailySpend.Float64
	}
	if notifications.Valid {
		b.Notifications = json.RawMessage(notifications.String)
	}

	return &b, nil
}

func (r *Runner) loadProjectBudgetForTask(ctx context.Context, taskID string) (*models.Budget, error) {
	// Get project ID from task
	var projectID string
	err := r.db.QueryRowContext(ctx, `SELECT project_id FROM tasks WHERE id = $1`, taskID).Scan(&projectID)
	if err != nil {
		return nil, err
	}

	var b models.Budget
	var pid, tid sql.NullString
	var maxCost, maxDailySpend sql.NullFloat64
	var maxRuntime, maxModelCalls, maxToolCalls, maxShellCommands, maxConcurrent sql.NullInt32
	var notifications sql.NullString

	err = r.db.QueryRowContext(ctx, `
		SELECT id, organization_id, project_id, task_id, type, period,
		       max_cost, max_runtime_minutes, max_model_calls, max_tool_calls,
		       max_shell_commands, max_concurrent_agents, max_daily_spend,
		       notifications, created_at, updated_at
		FROM budgets WHERE project_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, projectID).Scan(
		&b.ID, &b.OrganizationID, &pid, &tid, &b.Type, &b.Period,
		&maxCost, &maxRuntime, &maxModelCalls, &maxToolCalls,
		&maxShellCommands, &maxConcurrent, &maxDailySpend,
		&notifications, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if pid.Valid {
		b.ProjectID = &pid.String
	}
	if tid.Valid {
		b.TaskID = &tid.String
	}
	if maxCost.Valid {
		b.MaxCost = &maxCost.Float64
	}
	if maxRuntime.Valid {
		b.MaxRuntimeMinutes = int(maxRuntime.Int32)
	}
	if maxModelCalls.Valid {
		b.MaxModelCalls = int(maxModelCalls.Int32)
	}
	if maxToolCalls.Valid {
		b.MaxToolCalls = int(maxToolCalls.Int32)
	}
	if maxShellCommands.Valid {
		b.MaxShellCommands = int(maxShellCommands.Int32)
	}
	if maxConcurrent.Valid {
		b.MaxConcurrentAgents = int(maxConcurrent.Int32)
	}
	if maxDailySpend.Valid {
		b.MaxDailySpend = &maxDailySpend.Float64
	}
	if notifications.Valid {
		b.Notifications = json.RawMessage(notifications.String)
	}

	return &b, nil
}

func (r *Runner) recordModelUsage(ctx context.Context, run *models.AgentRun, task *models.Task, result *modelrouter.CallResult) error {
	if r.db == nil || run == nil || task == nil || result == nil {
		return nil
	}
	model := result.Model
	if model == "" && run.Model != nil {
		model = *run.Model
	}
	provider := result.Provider
	if provider == "" && run.Provider != nil {
		provider = *run.Provider
	}
	if model == "" {
		model = "unknown"
	}
	if provider == "" {
		provider = "unknown"
	}
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO model_usage (
			id, agent_run_id, task_id, model, provider, prompt_tokens,
			completion_tokens, total_tokens, cost, latency_ms, success, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, uuid.New().String(), run.ID, task.ID, model, provider, result.PromptTokens,
		result.CompletionTokens, result.TotalTokens, result.Cost, result.LatencyMs, true, now)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET model = $1,
		    provider = $2,
		    prompt_tokens = COALESCE(prompt_tokens, 0) + $3,
		    completion_tokens = COALESCE(completion_tokens, 0) + $4,
		    total_cost = COALESCE(total_cost, 0) + $5,
		    updated_at = $6
		WHERE id = $7
	`, model, provider, result.PromptTokens, result.CompletionTokens, result.Cost, now, run.ID)
	return err
}