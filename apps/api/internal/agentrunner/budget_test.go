package agentrunner

import (
	"context"
	"log/slog"
	"testing"

	"github.com/ai-dev-control-plane/models"
)

func TestLoadTaskBudget(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()

	workspacePath := t.TempDir()
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)

	_, err := db.Exec(`
		INSERT INTO budgets (
			id, organization_id, project_id, task_id, type, period,
			max_cost, max_runtime_minutes, max_model_calls, max_tool_calls,
			max_shell_commands, max_concurrent_agents, max_daily_spend
		) VALUES (
			'budget-1', 'org-1', 'project-1', 'task-1', 'task', 'per_run',
			1.50, 30, 100, 200, 50, 5, 10.00
		)
	`)
	if err != nil {
		t.Fatalf("insert task budget: %v", err)
	}

	runner := NewRunner(db, nil, nil, nil, nil, slog.Default())
	budget, err := runner.loadTaskBudget(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("loadTaskBudget error: %v", err)
	}
	if budget == nil {
		t.Fatal("expected budget, got nil")
	}
	if budget.Type != "task" {
		t.Errorf("type = %q, want task", budget.Type)
	}
	if budget.Period != "per_run" {
		t.Errorf("period = %q, want per_run", budget.Period)
	}
	if budget.MaxCost == nil || *budget.MaxCost != 1.50 {
		t.Errorf("max_cost = %v, want 1.50", budget.MaxCost)
	}
	if budget.MaxRuntimeMinutes != 30 {
		t.Errorf("max_runtime_minutes = %d, want 30", budget.MaxRuntimeMinutes)
	}
	if budget.MaxModelCalls != 100 {
		t.Errorf("max_model_calls = %d, want 100", budget.MaxModelCalls)
	}
	if budget.TaskID == nil || *budget.TaskID != "task-1" {
		t.Errorf("task_id = %v, want task-1", budget.TaskID)
	}
}

func TestLoadProjectBudgetForTask(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()

	workspacePath := t.TempDir()
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)

	_, err := db.Exec(`
		INSERT INTO budgets (
			id, organization_id, project_id, task_id, type, period,
			max_cost, max_runtime_minutes, max_model_calls, max_tool_calls,
			max_shell_commands, max_concurrent_agents, max_daily_spend
		) VALUES (
			'budget-2', 'org-1', 'project-1', NULL, 'project', 'daily',
			5.00, 60, 500, 1000, 100, 10, 25.00
		)
	`)
	if err != nil {
		t.Fatalf("insert project budget: %v", err)
	}

	runner := NewRunner(db, nil, nil, nil, nil, slog.Default())
	budget, err := runner.loadProjectBudgetForTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("loadProjectBudgetForTask error: %v", err)
	}
	if budget == nil {
		t.Fatal("expected budget, got nil")
	}
	if budget.Type != "project" {
		t.Errorf("type = %q, want project", budget.Type)
	}
	if budget.Period != "daily" {
		t.Errorf("period = %q, want daily", budget.Period)
	}
	if budget.ProjectID == nil || *budget.ProjectID != "project-1" {
		t.Errorf("project_id = %v, want project-1", budget.ProjectID)
	}
}

func TestLoadTaskBudgetFallsBackToProjectBudget(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()

	workspacePath := t.TempDir()
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)

	_, err := db.Exec(`
		INSERT INTO budgets (
			id, organization_id, project_id, task_id, type, period,
			max_cost, max_runtime_minutes, max_model_calls, max_tool_calls,
			max_shell_commands, max_concurrent_agents, max_daily_spend
		) VALUES (
			'budget-3', 'org-1', 'project-1', NULL, 'project', 'monthly',
			7.00, 120, 700, 1400, 150, 15, 35.00
		)
	`)
	if err != nil {
		t.Fatalf("insert project budget: %v", err)
	}

	runner := NewRunner(db, nil, nil, nil, nil, slog.Default())
	budget, err := runner.loadTaskBudget(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("loadTaskBudget fallback error: %v", err)
	}
	if budget == nil {
		t.Fatal("expected project budget fallback, got nil")
	}
	if budget.Type != "project" {
		t.Errorf("type = %q, want project", budget.Type)
	}
}
