package budget

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/ai-dev-control-plane/models"
)

// Engine enforces budget constraints on agent runs.
type Engine struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewEngine creates a new budget engine.
// db may be nil; in that case budget checks are best-effort.
func NewEngine(db *sql.DB) *Engine {
	return &Engine{db: db}
}

// WithLogger attaches a logger to the engine.
func (e *Engine) WithLogger(logger *slog.Logger) *Engine {
	e.logger = logger
	return e
}

// RunState tracks the current state of an agent run for budget checking.
type RunState struct {
	CostSoFar       float64
	DurationMinutes int
	ModelCalls      int
	ToolCalls       int
	ShellCommands   int
	FilesChanged    int
	DiffSizeKB      int
}

// CheckResult contains the outcome of a budget check.
type CheckResult struct {
	Allowed    bool
	Reason     string
	Remaining  float64
	Violations []string
}

// CheckRun verifies if a run is within budget constraints.
func (e *Engine) CheckRun(ctx context.Context, budget *models.Budget, runState *RunState) (*CheckResult, error) {
	if budget == nil || budget.IsUnlimited() {
		return &CheckResult{Allowed: true, Remaining: -1}, nil
	}

	result := &CheckResult{
		Allowed:    true,
		Violations: []string{},
	}

	// Check per-run limits
	if budget.MaxCost != nil && runState != nil {
		if runState.CostSoFar > *budget.MaxCost {
			result.Allowed = false
			result.Violations = append(result.Violations,
				fmt.Sprintf("cost %.4f exceeds max cost %.4f", runState.CostSoFar, *budget.MaxCost))
		} else {
			result.Remaining = *budget.MaxCost - runState.CostSoFar
		}
	}

	if budget.MaxRuntimeMinutes > 0 && runState != nil {
		if runState.DurationMinutes > budget.MaxRuntimeMinutes {
			result.Allowed = false
			result.Violations = append(result.Violations,
				fmt.Sprintf("runtime %d min exceeds max %d min", runState.DurationMinutes, budget.MaxRuntimeMinutes))
		}
	}

	if budget.MaxModelCalls > 0 && runState != nil {
		if runState.ModelCalls > budget.MaxModelCalls {
			result.Allowed = false
			result.Violations = append(result.Violations,
				fmt.Sprintf("model calls %d exceed max %d", runState.ModelCalls, budget.MaxModelCalls))
		}
	}

	if budget.MaxToolCalls > 0 && runState != nil {
		if runState.ToolCalls > budget.MaxToolCalls {
			result.Allowed = false
			result.Violations = append(result.Violations,
				fmt.Sprintf("tool calls %d exceed max %d", runState.ToolCalls, budget.MaxToolCalls))
		}
	}

	if budget.MaxShellCommands > 0 && runState != nil {
		if runState.ShellCommands > budget.MaxShellCommands {
			result.Allowed = false
			result.Violations = append(result.Violations,
				fmt.Sprintf("shell commands %d exceed max %d", runState.ShellCommands, budget.MaxShellCommands))
		}
	}

	// Check per-period limits (require DB)
	if e.db != nil {
		// Check daily spend
		if budget.MaxDailySpend != nil {
			dailySpend, err := e.GetDailySpend(ctx, budget.OrganizationID)
			if err != nil {
				e.logWarn("failed to get daily spend", "error", err)
			} else if dailySpend > *budget.MaxDailySpend {
				result.Allowed = false
				result.Violations = append(result.Violations,
					fmt.Sprintf("daily spend %.4f exceeds max %.4f", dailySpend, *budget.MaxDailySpend))
			}
		}

		// Check concurrent runs for project-level budgets
		if budget.MaxConcurrentAgents > 0 && budget.ProjectID != nil {
			concurrent, err := e.GetConcurrentRuns(ctx, *budget.ProjectID)
			if err != nil {
				e.logWarn("failed to get concurrent runs", "error", err)
			} else if concurrent > budget.MaxConcurrentAgents {
				result.Allowed = false
				result.Violations = append(result.Violations,
					fmt.Sprintf("concurrent runs %d exceed max %d", concurrent, budget.MaxConcurrentAgents))
			}
		}
	}

	if !result.Allowed {
		result.Reason = fmt.Sprintf("budget violations: %v", result.Violations)
	}

	return result, nil
}

// RecordUsage persists model usage for budget tracking.
func (e *Engine) RecordUsage(ctx context.Context, runID, taskID, model, provider string, promptTokens, completionTokens int, cost float64, latencyMs int) error {
	if e.db == nil {
		return nil // Best-effort when no DB
	}

	_, err := e.db.ExecContext(ctx, `
		INSERT INTO model_usage (
			id, run_id, task_id, model, provider,
			prompt_tokens, completion_tokens, cost, latency_ms, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
	`, generateID(), runID, taskID, model, provider, promptTokens, completionTokens, cost, latencyMs, time.Now().UTC())

	if err != nil {
		return fmt.Errorf("record model usage: %w", err)
	}

	return nil
}

// GetDailySpend returns total spend for an organization today.
func (e *Engine) GetDailySpend(ctx context.Context, orgID string) (float64, error) {
	if e.db == nil {
		return 0, nil
	}

	since := time.Now().UTC().Truncate(24 * time.Hour)
	var total sql.NullFloat64
	err := e.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(cost), 0)
		FROM model_usage mu
		JOIN agent_runs ar ON mu.run_id = ar.id
		WHERE ar.organization_id = $1
		  AND mu.created_at >= $2
	`, orgID, since).Scan(&total)

	if err != nil {
		return 0, fmt.Errorf("get daily spend: %w", err)
	}

	if total.Valid {
		return total.Float64, nil
	}
	return 0, nil
}

// GetConcurrentRuns returns the number of currently running agents for a project.
func (e *Engine) GetConcurrentRuns(ctx context.Context, projectID string) (int, error) {
	if e.db == nil {
		return 0, nil
	}

	var count int
	err := e.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM agent_runs ar
		JOIN tasks t ON ar.task_id = t.id
		WHERE t.project_id = $1
		  AND ar.status = 'running'
	`, projectID).Scan(&count)

	if err != nil {
		return 0, fmt.Errorf("get concurrent runs: %w", err)
	}

	return count, nil
}

// GetRunCost returns the total cost so far for a specific run.
func (e *Engine) GetRunCost(ctx context.Context, runID string) (float64, error) {
	if e.db == nil {
		return 0, nil
	}

	var total sql.NullFloat64
	err := e.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(cost), 0)
		FROM model_usage
		WHERE run_id = $1
	`, runID).Scan(&total)

	if err != nil {
		return 0, fmt.Errorf("get run cost: %w", err)
	}

	if total.Valid {
		return total.Float64, nil
	}
	return 0, nil
}

// GetTaskCost returns the total cost so far for a specific task.
func (e *Engine) GetTaskCost(ctx context.Context, taskID string) (float64, error) {
	if e.db == nil {
		return 0, nil
	}

	var total sql.NullFloat64
	err := e.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(cost), 0)
		FROM model_usage mu
		JOIN agent_runs ar ON mu.run_id = ar.id
		WHERE ar.task_id = $1
	`, taskID).Scan(&total)

	if err != nil {
		return 0, fmt.Errorf("get task cost: %w", err)
	}

	if total.Valid {
		return total.Float64, nil
	}
	return 0, nil
}

// GetPeriodSpend returns total spend for an organization in the given period.
func (e *Engine) GetPeriodSpend(ctx context.Context, orgID string, period string) (float64, error) {
	if e.db == nil {
		return 0, nil
	}

	var since time.Time
	now := time.Now().UTC()
	switch period {
	case models.BudgetPeriodDaily:
		since = now.Truncate(24 * time.Hour)
	case models.BudgetPeriodWeekly:
		since = now.AddDate(0, 0, -int(now.Weekday()))
		since = since.Truncate(24 * time.Hour)
	case models.BudgetPeriodMonthly:
		since = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		since = now.Truncate(24 * time.Hour)
	}

	var total sql.NullFloat64
	err := e.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(cost), 0)
		FROM model_usage mu
		JOIN agent_runs ar ON mu.run_id = ar.id
		WHERE ar.organization_id = $1
		  AND mu.created_at >= $2
	`, orgID, since).Scan(&total)

	if err != nil {
		return 0, fmt.Errorf("get period spend: %w", err)
	}

	if total.Valid {
		return total.Float64, nil
	}
	return 0, nil
}

// logWarn logs a warning message if a logger is attached.
func (e *Engine) logWarn(msg string, args ...any) {
	if e.logger != nil {
		e.logger.Warn(msg, args...)
	}
}

// generateID generates a unique identifier for usage records.
// Uses timestamp-based fallback when no UUID library is available.
func generateID() string {
	return fmt.Sprintf("mu-%d-%d", time.Now().UnixNano(), time.Now().Unix())
}
