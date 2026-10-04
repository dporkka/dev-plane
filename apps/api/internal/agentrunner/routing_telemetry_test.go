package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/modelrouter"
	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/models"
)

func TestNextModelActionSendsStableAutoRouteContext(t *testing.T) {
	provider := &fakeModelProvider{responses: []string{
		`{"action":"final_response","content":"done"}`,
	}}
	runner := NewRunner(nil, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))

	run := &models.AgentRun{ID: "run-1", TaskID: "task-1", AgentRole: models.AgentRoleSecurity}
	task := &models.Task{ID: "task-1", RiskLevel: models.RiskLevelCritical}

	if _, _, err := runner.nextModelAction(context.Background(), run, task, "system", nil, nil); err != nil {
		t.Fatalf("nextModelAction() error: %v", err)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(provider.calls))
	}
	req := provider.calls[0]
	if req.Route != modelrouter.RouteAuto {
		t.Fatalf("route = %q, want %q", req.Route, modelrouter.RouteAuto)
	}
	if req.RoutingMetadata["agent-role"] != models.AgentRoleSecurity {
		t.Fatalf("agent-role metadata = %q", req.RoutingMetadata["agent-role"])
	}
	if req.RoutingMetadata["risk"] != string(models.RiskLevelCritical) {
		t.Fatalf("risk metadata = %q", req.RoutingMetadata["risk"])
	}
}

func TestRoutingDecisionTelemetryCapturesVerifierOutcome(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()
	createRoutingDecisionTestTable(t, db)

	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default())
	run := &models.AgentRun{ID: "run-1", TaskID: "task-1", AgentRole: models.AgentRoleImplementer}
	task := &models.Task{ID: "task-1", RiskLevel: models.RiskLevelHigh}
	result := &modelrouter.CallResult{
		Route:            modelrouter.RouteCodingDeep,
		RouteSource:      modelrouter.RouteSourceExplicit,
		PolicyVersion:    modelrouter.SemanticRoutingPolicyVersion,
		Model:            "deepseek/deepseek-v4.1-flash",
		Provider:         "bifrost",
		SpendAuthority:   modelrouter.SpendAuthorityGateway,
		PromptTokens:     100,
		CompletionTokens: 25,
		TotalTokens:      125,
		Cost:             0,
		LatencyMs:        420,
	}

	if err := runner.recordRoutingDecision(context.Background(), run, task, 7, result); err != nil {
		t.Fatalf("recordRoutingDecision() error: %v", err)
	}

	checks := map[string]any{
		"tests": map[string]any{"passed": true, "exit_code": float64(0)},
		"passed": true,
	}
	if err := runner.recordRoutingOutcome(context.Background(), run.ID, models.AgentRunStatusCompleted, checks, ""); err != nil {
		t.Fatalf("recordRoutingOutcome() error: %v", err)
	}

	var route, source, policyVersion, taskType, difficulty, role, risk, model, provider, spendAuthority, outcome string
	var step, prompt, completion, total, latency int
	var estimatedCost float64
	var providerSucceeded, verifierPassed, humanIntervention bool
	var verifierJSON string
	if err := db.QueryRow(`
		SELECT step_number, route, route_source, policy_version, task_type, difficulty,
		       agent_role, risk_level, model, provider, spend_authority,
		       prompt_tokens, completion_tokens, total_tokens, estimated_cost, latency_ms,
		       provider_call_succeeded, outcome_status, verifier_passed,
		       verifier_results, human_intervention_required
		FROM routing_decisions WHERE agent_run_id = 'run-1'
	`).Scan(
		&step, &route, &source, &policyVersion, &taskType, &difficulty,
		&role, &risk, &model, &provider, &spendAuthority,
		&prompt, &completion, &total, &estimatedCost, &latency,
		&providerSucceeded, &outcome, &verifierPassed, &verifierJSON, &humanIntervention,
	); err != nil {
		t.Fatalf("query routing decision: %v", err)
	}

	if step != 7 || route != modelrouter.RouteCodingDeep || source != modelrouter.RouteSourceExplicit {
		t.Fatalf("routing identity = step %d route %q source %q", step, route, source)
	}
	if policyVersion != modelrouter.SemanticRoutingPolicyVersion {
		t.Fatalf("policy version = %q", policyVersion)
	}
	if taskType != modelrouter.TaskTypeCode || difficulty != modelrouter.DifficultyHard {
		t.Fatalf("task routing context = %q/%q", taskType, difficulty)
	}
	if role != models.AgentRoleImplementer || risk != string(models.RiskLevelHigh) {
		t.Fatalf("agent context = %q/%q", role, risk)
	}
	if model != result.Model || provider != result.Provider || spendAuthority != modelrouter.SpendAuthorityGateway {
		t.Fatalf("resolved target = %q/%q authority=%q", provider, model, spendAuthority)
	}
	if prompt != 100 || completion != 25 || total != 125 || estimatedCost != 0 || latency != 420 {
		t.Fatalf("usage telemetry mismatch")
	}
	if !providerSucceeded || outcome != models.AgentRunStatusCompleted || !verifierPassed || humanIntervention {
		t.Fatalf("outcome telemetry = provider=%v outcome=%q verifier=%v intervention=%v", providerSucceeded, outcome, verifierPassed, humanIntervention)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(verifierJSON), &decoded); err != nil {
		t.Fatalf("decode verifier results: %v", err)
	}
	if passed, _ := decoded["passed"].(bool); !passed {
		t.Fatalf("verifier results did not preserve passed=true: %s", verifierJSON)
	}
}

func TestFinalChecksPassedUsesStructuredToolResult(t *testing.T) {
	if finalChecksPassed(map[string]any{
		"tests": map[string]any{
			"output": `{"passed":false,"exit_code":1}`,
			"error":  "<nil>",
		},
	}) {
		t.Fatal("expected failed test payload to produce verifier failure even when tool call returned nil error")
	}
	if !finalChecksPassed(map[string]any{
		"tests": map[string]any{
			"output": `{"passed":true,"exit_code":0}`,
			"error":  "<nil>",
		},
	}) {
		t.Fatal("expected passed test payload to produce verifier success")
	}
}

func createRoutingDecisionTestTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE routing_decisions (
			id TEXT PRIMARY KEY,
			agent_run_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			step_number INTEGER NOT NULL,
			route TEXT NOT NULL,
			route_source TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			task_type TEXT NOT NULL,
			difficulty TEXT NOT NULL,
			agent_role TEXT NOT NULL,
			risk_level TEXT NOT NULL,
			model TEXT NOT NULL,
			provider TEXT NOT NULL,
			spend_authority TEXT NOT NULL,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			estimated_cost REAL NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			provider_call_succeeded BOOLEAN NOT NULL DEFAULT true,
			outcome_status TEXT NOT NULL DEFAULT 'pending',
			verifier_passed BOOLEAN,
			verifier_results TEXT DEFAULT '{}',
			human_intervention_required BOOLEAN NOT NULL DEFAULT false,
			error TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			outcome_recorded_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("create routing_decisions test table: %v", err)
	}
}
