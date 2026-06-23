package agentrunner

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/internal/modelrouter"
	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

func TestRunUsesModelDrivenToolActions(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()

	workspacePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspacePath, "README.md"), []byte("# Project\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)

	provider := &fakeModelProvider{responses: []string{
		`{"action":"tool_call","tool_name":"read_file","tool_input":{"path":"README.md"}}`,
		`{"action":"final_response","content":"Read the project README."}`,
	}}
	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))

	if err := runner.Run(context.Background(), "run-1"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(provider.calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(provider.calls))
	}

	var readFileSteps int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_steps WHERE agent_run_id = 'run-1' AND tool_name = 'read_file'`).Scan(&readFileSteps); err != nil {
		t.Fatalf("query read_file steps: %v", err)
	}
	if readFileSteps != 1 {
		t.Fatalf("read_file steps = %d, want 1", readFileSteps)
	}

	var hardcodedInspectSteps int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_steps WHERE agent_run_id = 'run-1' AND tool_name = 'inspect_repo'`).Scan(&hardcodedInspectSteps); err != nil {
		t.Fatalf("query inspect_repo steps: %v", err)
	}
	if hardcodedInspectSteps != 0 {
		t.Fatalf("inspect_repo steps = %d, want 0; runner should follow model actions", hardcodedInspectSteps)
	}

	var usageRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM model_usage WHERE agent_run_id = 'run-1'`).Scan(&usageRows); err != nil {
		t.Fatalf("query model_usage: %v", err)
	}
	if usageRows != 2 {
		t.Fatalf("model_usage rows = %d, want 2", usageRows)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-1'`).Scan(&status); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if status != models.AgentRunStatusCompleted {
		t.Fatalf("run status = %q, want completed", status)
	}
}

func TestRunPersistsModelHandoffToMailbox(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()

	workspacePath := t.TempDir()
	insertRunnerFixture(t, db, workspacePath, models.AgentRolePlanner)

	provider := &fakeModelProvider{responses: []string{
		`{"action":"handoff","to_agent":"implementer","message_type":"handoff","content":"Implement the README update.","metadata":{"stage":"spec"}}`,
	}}
	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))

	if err := runner.Run(context.Background(), "run-1"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	var fromAgent, toAgent, messageType, content string
	if err := db.QueryRow(`
		SELECT from_agent, to_agent, message_type, content
		FROM agent_messages
		WHERE task_id = 'task-1'
	`).Scan(&fromAgent, &toAgent, &messageType, &content); err != nil {
		t.Fatalf("query mailbox: %v", err)
	}
	if fromAgent != models.AgentRolePlanner || toAgent != models.AgentRoleImplementer || messageType != models.MessageTypeHandoff {
		t.Fatalf("mailbox route = %s -> %s (%s), want planner -> implementer (handoff)", fromAgent, toAgent, messageType)
	}
	if content != "Implement the README update." {
		t.Fatalf("mailbox content = %q", content)
	}
}

func TestRunModelApprovalRequestCreatesApprovalAndPauses(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()

	workspacePath := t.TempDir()
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)

	provider := &fakeModelProvider{responses: []string{
		`{"action":"request_approval","content":"Need a human to approve this risky change."}`,
	}}
	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))

	if err := runner.Run(context.Background(), "run-1"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	var status, errorMessage string
	if err := db.QueryRow(`SELECT status, error_message FROM agent_runs WHERE id = 'run-1'`).Scan(&status, &errorMessage); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if status != models.AgentRunStatusPaused {
		t.Fatalf("run status = %q, want paused", status)
	}
	if errorMessage != "Need a human to approve this risky change." {
		t.Fatalf("error message = %q", errorMessage)
	}

	var approvalType, requestedBy, metadata string
	if err := db.QueryRow(`
		SELECT approval_type, requested_by, metadata
		FROM approvals
		WHERE task_id = 'task-1' AND agent_run_id = 'run-1'
	`).Scan(&approvalType, &requestedBy, &metadata); err != nil {
		t.Fatalf("query approval: %v", err)
	}
	if approvalType != models.ApprovalTypeRiskyAction {
		t.Fatalf("approval_type = %q, want risky_action", approvalType)
	}
	if requestedBy != "user-1" {
		t.Fatalf("requested_by = %q, want user-1", requestedBy)
	}
	if !strings.Contains(metadata, "model_request") || !strings.Contains(metadata, "risky change") {
		t.Fatalf("metadata = %s, want model request reason", metadata)
	}

	var approvalSteps int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM agent_steps
		WHERE agent_run_id = 'run-1' AND step_type = ?
	`, models.AgentStepTypeApprovalRequest).Scan(&approvalSteps); err != nil {
		t.Fatalf("query approval steps: %v", err)
	}
	if approvalSteps != 1 {
		t.Fatalf("approval steps = %d, want 1", approvalSteps)
	}
}

func TestRunDeniedToolOperationFailsWithoutApproval(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()

	workspacePath := t.TempDir()
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)

	provider := &fakeModelProvider{responses: []string{
		`{"action":"tool_call","tool_name":"write_file","tool_input":{"path":"BLOCKED.md","content":"must not be written"}}`,
	}}
	denyWrites := policies.NewEngine([]policies.Policy{
		{Name: "deny_file_writes", ResourceType: "file", Action: "write", Effect: policies.EffectDeny},
	})
	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), denyWrites, nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))

	err := runner.Run(context.Background(), "run-1")
	if err == nil {
		t.Fatal("expected denied operation error")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Fatalf("error = %v, want failed run", err)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-1'`).Scan(&status); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if status != models.AgentRunStatusFailed {
		t.Fatalf("run status = %q, want failed", status)
	}

	var approvals int
	if err := db.QueryRow(`SELECT COUNT(*) FROM approvals WHERE task_id = 'task-1'`).Scan(&approvals); err != nil {
		t.Fatalf("query approvals: %v", err)
	}
	if approvals != 0 {
		t.Fatalf("approvals = %d, want 0 for denied operation", approvals)
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "BLOCKED.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("BLOCKED.md stat error = %v, want not exist", err)
	}
}

func TestRunResumedPausedRunLoadsHistoryAndContinuesStepNumbering(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()

	workspacePath := t.TempDir()
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)
	_, err := db.Exec(`
		UPDATE agent_runs SET status = ?, total_cost = 0.25 WHERE id = 'run-1';
		INSERT INTO agent_steps (
			id, agent_run_id, step_number, step_type, status, content, cost
		) VALUES (
			'step-1', 'run-1', 1, ?, ?, 'Need approval before continuing.', 0.01
		);
	`, models.AgentRunStatusQueued, models.AgentStepTypeApprovalRequest, models.AgentStepStatusCompleted)
	if err != nil {
		t.Fatalf("insert paused history: %v", err)
	}

	provider := &fakeModelProvider{responses: []string{
		`{"action":"final_response","content":"Continuing after approval."}`,
	}}
	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))

	if err := runner.Run(context.Background(), "run-1"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(provider.calls))
	}
	if len(provider.calls[0].Messages) < 2 || !strings.Contains(provider.calls[0].Messages[1].Content, "Need approval before continuing.") {
		t.Fatalf("model prompt did not include prior approval history: %+v", provider.calls[0].Messages)
	}

	var finalStepNumber int
	if err := db.QueryRow(`
		SELECT step_number
		FROM agent_steps
		WHERE agent_run_id = 'run-1' AND content = 'Continuing after approval.'
	`).Scan(&finalStepNumber); err != nil {
		t.Fatalf("query final step: %v", err)
	}
	if finalStepNumber != 2 {
		t.Fatalf("final step number = %d, want 2", finalStepNumber)
	}
}

func testRouterConfig() *modelrouter.Config {
	return &modelrouter.Config{
		DefaultModel:     "fake-coder",
		DefaultProvider:  "fake",
		MaxCostPer1K:     1,
		ProviderPriority: []string{"fake"},
	}
}

type fakeModelProvider struct {
	responses []string
	calls     []modelrouter.CallRequest
}

func (p *fakeModelProvider) Name() string      { return "fake" }
func (p *fakeModelProvider) IsAvailable() bool { return true }
func (p *fakeModelProvider) Models() []modelrouter.ModelInfo {
	return []modelrouter.ModelInfo{{
		Name:                     "fake-coder",
		Provider:                 "fake",
		MaxContext:               128000,
		CodingStrength:           9,
		ReasoningStrength:        9,
		LatencyMs:                10,
		CostPer1KInput:           0.001,
		CostPer1KOutput:          0.001,
		SupportsStructuredOutput: true,
		SupportsFunctionCalling:  true,
	}}
}
func (p *fakeModelProvider) Call(ctx context.Context, req modelrouter.CallRequest) (*modelrouter.CallResult, error) {
	p.calls = append(p.calls, req)
	if len(p.responses) == 0 {
		return nil, errors.New("fake model response exhausted")
	}
	content := p.responses[0]
	p.responses = p.responses[1:]
	return &modelrouter.CallResult{
		Content:          content,
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		Cost:             0.001,
		LatencyMs:        10,
		FinishReason:     "stop",
	}, nil
}

func setupRunnerOrchestrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL
		);
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			repository_id TEXT NOT NULL,
			workspace_id TEXT,
			created_by TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'web',
			source_id TEXT,
			title TEXT NOT NULL,
			description TEXT,
			status TEXT NOT NULL DEFAULT 'approved',
			priority TEXT NOT NULL DEFAULT 'medium',
			risk_level TEXT NOT NULL DEFAULT 'low',
			target_branch TEXT NOT NULL DEFAULT 'main',
			spec TEXT,
			acceptance_criteria TEXT DEFAULT '[]',
			max_cost REAL,
			max_runtime_minutes INTEGER DEFAULT 60,
			approval_requirements TEXT DEFAULT '[]',
			metadata TEXT DEFAULT '{}',
			started_at DATETIME,
			completed_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			deleted_at DATETIME
		);
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			task_id TEXT,
			name TEXT NOT NULL,
			branch TEXT NOT NULL,
			base_branch TEXT NOT NULL DEFAULT 'main',
			worktree_path TEXT,
			runtime_provider TEXT NOT NULL DEFAULT 'local',
			runtime_session_id TEXT,
			status TEXT NOT NULL DEFAULT 'ready',
			preview_url TEXT,
			settings TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			deleted_at DATETIME
		);
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT,
			agent_role TEXT NOT NULL DEFAULT 'implementer',
			model TEXT,
			provider TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			started_at DATETIME,
			completed_at DATETIME,
			prompt_tokens INTEGER DEFAULT 0,
			completion_tokens INTEGER DEFAULT 0,
			total_cost REAL DEFAULT 0,
			error_message TEXT,
			summary TEXT,
			metadata TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE agent_steps (
			id TEXT PRIMARY KEY,
			agent_run_id TEXT NOT NULL,
			step_number INTEGER NOT NULL,
			step_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			content TEXT,
			tool_name TEXT,
			tool_input TEXT,
			tool_output TEXT,
			command TEXT,
			command_output TEXT,
			exit_code INTEGER,
			file_path TEXT,
			diff TEXT,
			cost REAL DEFAULT 0,
			latency_ms INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE model_usage (
			id TEXT PRIMARY KEY,
			agent_run_id TEXT,
			task_id TEXT NOT NULL,
			model TEXT NOT NULL,
			provider TEXT NOT NULL,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			cost REAL NOT NULL DEFAULT 0,
			latency_ms INTEGER DEFAULT 0,
			success BOOLEAN NOT NULL DEFAULT true,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE agent_messages (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_run_id TEXT,
			from_agent TEXT NOT NULL,
			to_agent TEXT NOT NULL,
			message_type TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE approvals (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_run_id TEXT,
			approval_type TEXT NOT NULL,
			requested_by TEXT NOT NULL,
			requested_at DATETIME NOT NULL,
			responded_by TEXT,
			response TEXT,
			response_note TEXT,
			responded_at DATETIME,
			expires_at DATETIME,
			metadata TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE budgets (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			project_id TEXT,
			task_id TEXT,
			type TEXT NOT NULL,
			period TEXT NOT NULL,
			max_cost REAL,
			max_runtime_minutes INTEGER DEFAULT 0,
			max_model_calls INTEGER DEFAULT 0,
			max_tool_calls INTEGER DEFAULT 0,
			max_shell_commands INTEGER DEFAULT 0,
			max_concurrent_agents INTEGER DEFAULT 0,
			max_daily_spend REAL,
			notifications TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create orchestration schema: %v", err)
	}
	return db
}

func insertRunnerFixture(t *testing.T, db *sql.DB, workspacePath, role string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO projects (id, organization_id) VALUES ('project-1', 'org-1');
		INSERT INTO workspaces (
			id, repository_id, task_id, name, branch, base_branch, worktree_path, runtime_provider, status
		) VALUES (
			'workspace-1', 'repo-1', 'task-1', 'Test workspace', 'feature/test', 'main', $1, 'local', 'ready'
		);
		INSERT INTO tasks (
			id, project_id, repository_id, workspace_id, created_by, source, title, status, priority,
			risk_level, target_branch, acceptance_criteria, approval_requirements, metadata
		) VALUES (
			'task-1', 'project-1', 'repo-1', 'workspace-1', 'user-1', 'web', 'Read README', 'approved', 'medium',
			'low', 'main', '[]', '[]', '{}'
		);
		INSERT INTO agent_runs (id, task_id, workspace_id, agent_role, status, metadata)
		VALUES ('run-1', 'task-1', 'workspace-1', $2, 'pending', '{}');
	`, workspacePath, role)
	if err != nil {
		t.Fatalf("insert runner fixture: %v", err)
	}
}
