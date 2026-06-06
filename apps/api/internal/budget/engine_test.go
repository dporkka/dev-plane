package budget

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/ai-dev-control-plane/models"
)

// ptrFloat64 returns a pointer to a float64 value.
func ptrFloat64(v float64) *float64 {
	return &v
}

// ptrString returns a pointer to a string value.
func ptrString(v string) *string {
	return &v
}

// setupTestDB creates an in-memory SQLite DB with required schema.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	// Create agent_runs table
	_, err = db.Exec(`
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT,
			organization_id TEXT,
			status TEXT,
			started_at DATETIME,
			completed_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("create agent_runs table: %v", err)
	}

	// Create tasks table
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			project_id TEXT
		)
	`)
	if err != nil {
		t.Fatalf("create tasks table: %v", err)
	}

	// Create model_usage table
	_, err = db.Exec(`
		CREATE TABLE model_usage (
			id TEXT PRIMARY KEY,
			run_id TEXT,
			task_id TEXT,
			model TEXT,
			provider TEXT,
			prompt_tokens INTEGER,
			completion_tokens INTEGER,
			cost REAL,
			latency_ms INTEGER,
			created_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("create model_usage table: %v", err)
	}

	return db
}

// makeBudget creates a Budget with all limit fields set for testing.
func makeBudget() *models.Budget {
	return &models.Budget{
		ID:                  "budget-1",
		OrganizationID:      "org-1",
		ProjectID:           ptrString("project-1"),
		Type:                models.BudgetTypeProject,
		Period:              models.BudgetPeriodDaily,
		MaxCost:             ptrFloat64(1.00),
		MaxRuntimeMinutes:   30,
		MaxModelCalls:       100,
		MaxToolCalls:        50,
		MaxShellCommands:    20,
		MaxConcurrentAgents: 5,
		MaxDailySpend:       ptrFloat64(10.00),
	}
}

// TestCheckRun_AllLimitsPass verifies a run within all limits is allowed.
func TestCheckRun_AllLimitsPass(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	// Seed data: daily spend is within limit
	_, err := db.Exec(`
		INSERT INTO agent_runs (id, task_id, organization_id, status) VALUES ('run-1', 'task-1', 'org-1', 'completed');
		INSERT INTO tasks (id, project_id) VALUES ('task-1', 'project-1');
		INSERT INTO model_usage (id, run_id, task_id, model, provider, prompt_tokens, completion_tokens, cost, latency_ms, created_at)
		VALUES ('mu-1', 'run-1', 'task-1', 'gpt-4o', 'openai', 1000, 500, 2.50, 1000, datetime('now'));
	`)
	if err != nil {
		t.Fatalf("seed data: %v", err)
	}

	runState := &RunState{
		CostSoFar:       0.50,
		DurationMinutes: 15,
		ModelCalls:      10,
		ToolCalls:       5,
		ShellCommands:   2,
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if !result.Allowed {
		t.Errorf("expected allowed, got denied: %s", result.Reason)
	}
	if result.Remaining != 0.50 {
		t.Errorf("expected remaining 0.50, got %.4f", result.Remaining)
	}
	if len(result.Violations) > 0 {
		t.Errorf("expected no violations, got %v", result.Violations)
	}
}

// TestCheckRun_CostExceeded verifies max_cost_per_run exceeded -> denied.
func TestCheckRun_CostExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	// Seed daily spend and concurrent data (they'll pass)
	seedPassingDBData(t, db)

	runState := &RunState{
		CostSoFar:       1.50, // exceeds MaxCost of 1.00
		DurationMinutes: 15,
		ModelCalls:      10,
		ToolCalls:       5,
		ShellCommands:   2,
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied when cost exceeded")
	}
	found := false
	for _, v := range result.Violations {
		if v == "cost 1.5000 exceeds max cost 1.0000" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected cost violation, got %v", result.Violations)
	}
}

// TestCheckRun_TimeExceeded verifies max_runtime exceeded -> denied.
func TestCheckRun_TimeExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	seedPassingDBData(t, db)

	runState := &RunState{
		CostSoFar:       0.50,
		DurationMinutes: 45, // exceeds MaxRuntimeMinutes of 30
		ModelCalls:      10,
		ToolCalls:       5,
		ShellCommands:   2,
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied when runtime exceeded")
	}
	found := false
	for _, v := range result.Violations {
		if v == "runtime 45 min exceeds max 30 min" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected runtime violation, got %v", result.Violations)
	}
}

// TestCheckRun_ModelCallsExceeded verifies max_model_calls exceeded -> denied.
func TestCheckRun_ModelCallsExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	seedPassingDBData(t, db)

	runState := &RunState{
		CostSoFar:       0.50,
		DurationMinutes: 15,
		ModelCalls:      150, // exceeds MaxModelCalls of 100
		ToolCalls:       5,
		ShellCommands:   2,
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied when model calls exceeded")
	}
	found := false
	for _, v := range result.Violations {
		if v == "model calls 150 exceed max 100" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected model calls violation, got %v", result.Violations)
	}
}

// TestCheckRun_ToolCallsExceeded verifies max_tool_calls exceeded -> denied.
func TestCheckRun_ToolCallsExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	seedPassingDBData(t, db)

	runState := &RunState{
		CostSoFar:       0.50,
		DurationMinutes: 15,
		ModelCalls:      10,
		ToolCalls:       75, // exceeds MaxToolCalls of 50
		ShellCommands:   2,
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied when tool calls exceeded")
	}
	found := false
	for _, v := range result.Violations {
		if v == "tool calls 75 exceed max 50" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tool calls violation, got %v", result.Violations)
	}
}

// TestCheckRun_ShellCommandsExceeded verifies max_shell_commands exceeded -> denied.
func TestCheckRun_ShellCommandsExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	seedPassingDBData(t, db)

	runState := &RunState{
		CostSoFar:       0.50,
		DurationMinutes: 15,
		ModelCalls:      10,
		ToolCalls:       5,
		ShellCommands:   25, // exceeds MaxShellCommands of 20
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied when shell commands exceeded")
	}
	found := false
	for _, v := range result.Violations {
		if v == "shell commands 25 exceed max 20" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected shell commands violation, got %v", result.Violations)
	}
}

// TestCheckRun_DailySpendExceeded verifies max_daily_spend exceeded -> denied.
func TestCheckRun_DailySpendExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	// Seed high daily spend
	_, err := db.Exec(`
		INSERT INTO agent_runs (id, task_id, organization_id, status) VALUES ('run-1', 'task-1', 'org-1', 'completed');
		INSERT INTO tasks (id, project_id) VALUES ('task-1', 'project-1');
		INSERT INTO model_usage (id, run_id, task_id, model, provider, prompt_tokens, completion_tokens, cost, latency_ms, created_at)
		VALUES ('mu-1', 'run-1', 'task-1', 'gpt-4o', 'openai', 1000, 500, 12.00, 1000, datetime('now'));
	`)
	if err != nil {
		t.Fatalf("seed data: %v", err)
	}

	// Seed a running agent for concurrent check
	_, err = db.Exec(`
		INSERT INTO agent_runs (id, task_id, organization_id, status)
		VALUES
			('run-2', 'task-1', 'org-1', 'running'),
			('run-3', 'task-1', 'org-1', 'running'),
			('run-4', 'task-1', 'org-1', 'running');
	`)
	if err != nil {
		t.Fatalf("seed running agents: %v", err)
	}

	runState := &RunState{
		CostSoFar:       0.50,
		DurationMinutes: 15,
		ModelCalls:      10,
		ToolCalls:       5,
		ShellCommands:   2,
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied when daily spend exceeded")
	}
	found := false
	for _, v := range result.Violations {
		if v == "daily spend 12.0000 exceeds max 10.0000" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected daily spend violation, got %v", result.Violations)
	}
}

// TestCheckRun_ConcurrentAgentsExceeded verifies max_concurrent_agents exceeded -> denied.
func TestCheckRun_ConcurrentAgentsExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	// Seed low daily spend (passes)
	_, err := db.Exec(`
		INSERT INTO agent_runs (id, task_id, organization_id, status) VALUES ('run-1', 'task-1', 'org-1', 'completed');
		INSERT INTO tasks (id, project_id) VALUES ('task-1', 'project-1');
		INSERT INTO model_usage (id, run_id, task_id, model, provider, prompt_tokens, completion_tokens, cost, latency_ms, created_at)
		VALUES ('mu-1', 'run-1', 'task-1', 'gpt-4o', 'openai', 1000, 500, 2.50, 1000, datetime('now'));
	`)
	if err != nil {
		t.Fatalf("seed data: %v", err)
	}

	// Seed 7 running agents for project-1, exceeds limit of 5
	_, err = db.Exec(`
		INSERT INTO agent_runs (id, task_id, organization_id, status)
		VALUES
			('run-2', 'task-1', 'org-1', 'running'),
			('run-3', 'task-1', 'org-1', 'running'),
			('run-4', 'task-1', 'org-1', 'running'),
			('run-5', 'task-1', 'org-1', 'running'),
			('run-6', 'task-1', 'org-1', 'running'),
			('run-7', 'task-1', 'org-1', 'running'),
			('run-8', 'task-1', 'org-1', 'running');
	`)
	if err != nil {
		t.Fatalf("seed running agents: %v", err)
	}

	runState := &RunState{
		CostSoFar:       0.50,
		DurationMinutes: 15,
		ModelCalls:      10,
		ToolCalls:       5,
		ShellCommands:   2,
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied when concurrent agents exceeded")
	}
	found := false
	for _, v := range result.Violations {
		if v == "concurrent runs 7 exceed max 5" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected concurrent agents violation, got %v", result.Violations)
	}
}

// TestRecordUsage verifies usage recording.
func TestRecordUsage(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()

	err := engine.RecordUsage(ctx, "run-1", "task-1", "gpt-4o", "openai", 1000, 500, 0.05, 2500)
	if err != nil {
		t.Fatalf("RecordUsage() error: %v", err)
	}

	// Verify the record was inserted
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM model_usage WHERE run_id = ? AND task_id = ? AND model = ?",
		"run-1", "task-1", "gpt-4o").Scan(&count)
	if err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 usage record, got %d", count)
	}
}

// TestRecordUsage_NoDB verifies RecordUsage returns nil when db is nil.
func TestRecordUsage_NoDB(t *testing.T) {
	engine := NewEngine(nil)
	ctx := context.Background()

	err := engine.RecordUsage(ctx, "run-1", "task-1", "gpt-4o", "openai", 1000, 500, 0.05, 2500)
	if err != nil {
		t.Errorf("RecordUsage() with nil db should return nil, got: %v", err)
	}
}

// TestGetDailySpend_NoDB verifies GetDailySpend returns 0 when db is nil.
func TestGetDailySpend_NoDB(t *testing.T) {
	engine := NewEngine(nil)
	ctx := context.Background()

	spend, err := engine.GetDailySpend(ctx, "org-1")
	if err != nil {
		t.Fatalf("GetDailySpend() error: %v", err)
	}
	if spend != 0 {
		t.Errorf("GetDailySpend() = %.4f, want 0", spend)
	}
}

// TestGetConcurrentRuns_NoDB verifies GetConcurrentRuns returns 0 when db is nil.
func TestGetConcurrentRuns_NoDB(t *testing.T) {
	engine := NewEngine(nil)
	ctx := context.Background()

	runs, err := engine.GetConcurrentRuns(ctx, "project-1")
	if err != nil {
		t.Fatalf("GetConcurrentRuns() error: %v", err)
	}
	if runs != 0 {
		t.Errorf("GetConcurrentRuns() = %d, want 0", runs)
	}
}

// TestGetRunCost_NoDB verifies GetRunCost returns 0 when db is nil.
func TestGetRunCost_NoDB(t *testing.T) {
	engine := NewEngine(nil)
	ctx := context.Background()

	cost, err := engine.GetRunCost(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRunCost() error: %v", err)
	}
	if cost != 0 {
		t.Errorf("GetRunCost() = %.4f, want 0", cost)
	}
}

// TestGetTaskCost_NoDB verifies GetTaskCost returns 0 when db is nil.
func TestGetTaskCost_NoDB(t *testing.T) {
	engine := NewEngine(nil)
	ctx := context.Background()

	cost, err := engine.GetTaskCost(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTaskCost() error: %v", err)
	}
	if cost != 0 {
		t.Errorf("GetTaskCost() = %.4f, want 0", cost)
	}
}

// TestCheckRun_NilBudget verifies nil budget is allowed.
func TestCheckRun_NilBudget(t *testing.T) {
	engine := NewEngine(nil)
	ctx := context.Background()

	result, err := engine.CheckRun(ctx, nil, &RunState{CostSoFar: 100})
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed with nil budget")
	}
	if result.Remaining != -1 {
		t.Errorf("expected remaining -1 (unlimited), got %.4f", result.Remaining)
	}
}

// TestCheckRun_UnlimitedBudget verifies unlimited budget is allowed.
func TestCheckRun_UnlimitedBudget(t *testing.T) {
	engine := NewEngine(nil)
	ctx := context.Background()

	budget := &models.Budget{
		OrganizationID: "org-1",
		Type:           models.BudgetTypeOrganization,
		Period:         models.BudgetPeriodDaily,
		// No limits set - all zero/nil
	}

	result, err := engine.CheckRun(ctx, budget, &RunState{CostSoFar: 999})
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed with unlimited budget")
	}
	if result.Remaining != -1 {
		t.Errorf("expected remaining -1 (unlimited), got %.4f", result.Remaining)
	}
}

// TestCheckRun_NoDB skips DB-dependent checks when db is nil.
func TestCheckRun_NoDB(t *testing.T) {
	engine := NewEngine(nil)
	ctx := context.Background()

	budget := &models.Budget{
		OrganizationID:      "org-1",
		ProjectID:           ptrString("project-1"),
		Type:                models.BudgetTypeProject,
		Period:              models.BudgetPeriodDaily,
		MaxCost:             ptrFloat64(1.00),
		MaxRuntimeMinutes:   30,
		MaxModelCalls:       100,
		MaxToolCalls:        50,
		MaxShellCommands:    20,
		MaxConcurrentAgents: 5,
		MaxDailySpend:       ptrFloat64(10.00),
	}

	runState := &RunState{
		CostSoFar:       0.50,
		DurationMinutes: 15,
		ModelCalls:      10,
		ToolCalls:       5,
		ShellCommands:   2,
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if !result.Allowed {
		t.Errorf("expected allowed when db is nil (per-run checks pass), got denied: %s", result.Reason)
	}
}

// TestCheckRun_NilRunState verifies nil run state still allows DB checks.
func TestCheckRun_NilRunState(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	seedPassingDBData(t, db)

	result, err := engine.CheckRun(ctx, budget, nil)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if !result.Allowed {
		t.Errorf("expected allowed with nil run state (per-run limits not checked), got denied: %s", result.Reason)
	}
}

// TestCheckRun_MultipleViolations verifies multiple violations are collected.
func TestCheckRun_MultipleViolations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	ctx := context.Background()
	budget := makeBudget()

	// Seed high daily spend (12.00 > 10.00 limit)
	_, err := db.Exec(`
		INSERT INTO agent_runs (id, task_id, organization_id, status) VALUES ('run-1', 'task-1', 'org-1', 'completed');
		INSERT INTO tasks (id, project_id) VALUES ('task-1', 'project-1');
		INSERT INTO model_usage (id, run_id, task_id, model, provider, prompt_tokens, completion_tokens, cost, latency_ms, created_at)
		VALUES ('mu-1', 'run-1', 'task-1', 'gpt-4o', 'openai', 1000, 500, 15.00, 1000, datetime('now'));
	`)
	if err != nil {
		t.Fatalf("seed data: %v", err)
	}

	// Seed 10 running agents for concurrent check
	_, err = db.Exec(`
		INSERT INTO agent_runs (id, task_id, organization_id, status)
		VALUES
			('run-2', 'task-1', 'org-1', 'running'),
			('run-3', 'task-1', 'org-1', 'running'),
			('run-4', 'task-1', 'org-1', 'running'),
			('run-5', 'task-1', 'org-1', 'running'),
			('run-6', 'task-1', 'org-1', 'running'),
			('run-7', 'task-1', 'org-1', 'running'),
			('run-8', 'task-1', 'org-1', 'running'),
			('run-9', 'task-1', 'org-1', 'running'),
			('run-10', 'task-1', 'org-1', 'running'),
			('run-11', 'task-1', 'org-1', 'running');
	`)
	if err != nil {
		t.Fatalf("seed running agents: %v", err)
	}

	runState := &RunState{
		CostSoFar:       2.00, // exceeds MaxCost 1.00
		DurationMinutes: 60,   // exceeds MaxRuntimeMinutes 30
		ModelCalls:      200,  // exceeds MaxModelCalls 100
		ToolCalls:       100,  // exceeds MaxToolCalls 50
		ShellCommands:   50,   // exceeds MaxShellCommands 20
	}

	result, err := engine.CheckRun(ctx, budget, runState)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied with multiple violations")
	}
	if len(result.Violations) < 5 {
		t.Errorf("expected at least 5 violations, got %d: %v", len(result.Violations), result.Violations)
	}
}

// TestNewEngine verifies engine creation.
func TestNewEngine(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	if engine == nil {
		t.Fatal("NewEngine() returned nil")
	}
	if engine.db != db {
		t.Error("db not set correctly")
	}

	// With nil db
	engine2 := NewEngine(nil)
	if engine2 == nil {
		t.Fatal("NewEngine(nil) returned nil")
	}
}

// TestEngine_WithLogger verifies logger attachment.
func TestEngine_WithLogger(t *testing.T) {
	engine := NewEngine(nil)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	engine.WithLogger(logger)
	if engine.logger != logger {
		t.Error("logger not set correctly")
	}
}

// seedPassingDBData inserts data that passes all DB-dependent checks.
func seedPassingDBData(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO agent_runs (id, task_id, organization_id, status) VALUES ('run-1', 'task-1', 'org-1', 'completed');
		INSERT INTO tasks (id, project_id) VALUES ('task-1', 'project-1');
		INSERT INTO model_usage (id, run_id, task_id, model, provider, prompt_tokens, completion_tokens, cost, latency_ms, created_at)
		VALUES ('mu-1', 'run-1', 'task-1', 'gpt-4o', 'openai', 1000, 500, 2.50, 1000, datetime('now'));
	`)
	if err != nil {
		t.Fatalf("seed data: %v", err)
	}

	// Seed 3 running agents (within limit of 5)
	_, err = db.Exec(`
		INSERT INTO agent_runs (id, task_id, organization_id, status)
		VALUES
			('run-2', 'task-1', 'org-1', 'running'),
			('run-3', 'task-1', 'org-1', 'running'),
			('run-4', 'task-1', 'org-1', 'running');
	`)
	if err != nil {
		t.Fatalf("seed running agents: %v", err)
	}
}
