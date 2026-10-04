package agentrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/modelrouter"
	"github.com/ai-dev-control-plane/models"
)

func (r *Runner) recordRoutingDecision(ctx context.Context, run *models.AgentRun, task *models.Task, stepNumber int, result *modelrouter.CallResult) error {
	if r == nil || r.db == nil || run == nil || task == nil || result == nil {
		return nil
	}
	if stepNumber <= 0 {
		if err := r.db.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(step_number), 0) + 1
			FROM agent_steps
			WHERE agent_run_id = $1
		`, run.ID).Scan(&stepNumber); err != nil {
			return fmt.Errorf("resolve routing step number: %w", err)
		}
	}

	route := strings.TrimSpace(result.Route)
	if route == "" {
		route = modelrouter.RouteAuto
	}
	routeSource := strings.TrimSpace(result.RouteSource)
	if routeSource == "" {
		routeSource = modelrouter.RouteSourceLegacyLocal
	}
	policyVersion := strings.TrimSpace(result.PolicyVersion)
	if policyVersion == "" {
		policyVersion = modelrouter.SemanticRoutingPolicyVersion
	}
	spendAuthority := strings.TrimSpace(result.SpendAuthority)
	if spendAuthority == "" {
		spendAuthority = modelrouter.SpendAuthorityDevPlane
	}
	model := strings.TrimSpace(result.Model)
	if model == "" && run.Model != nil {
		model = strings.TrimSpace(*run.Model)
	}
	if model == "" {
		model = "unknown"
	}
	provider := strings.TrimSpace(result.Provider)
	if provider == "" && run.Provider != nil {
		provider = strings.TrimSpace(*run.Provider)
	}
	if provider == "" {
		provider = "unknown"
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO routing_decisions (
			id, agent_run_id, task_id, step_number, route, route_source,
			policy_version, task_type, difficulty, agent_role, risk_level,
			model, provider, spend_authority, prompt_tokens, completion_tokens,
			total_tokens, estimated_cost, latency_ms, provider_call_succeeded,
			outcome_status, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16,
			$17, $18, $19, $20,
			'pending', $21
		)
	`, uuid.New().String(), run.ID, task.ID, stepNumber, route, routeSource,
		policyVersion, taskTypeForRole(run.AgentRole), difficultyForRisk(string(task.RiskLevel)), run.AgentRole, string(task.RiskLevel),
		model, provider, spendAuthority, result.PromptTokens, result.CompletionTokens,
		result.TotalTokens, result.Cost, result.LatencyMs, true, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("record routing decision: %w", err)
	}
	return nil
}

func (r *Runner) recordRoutingOutcome(ctx context.Context, runID, status string, verifierResults map[string]any, outcomeErr string) error {
	if r == nil || r.db == nil || strings.TrimSpace(runID) == "" {
		return nil
	}

	var verifierPassed any
	encodedVerifier := "{}"
	if verifierResults != nil {
		verifierPassed = finalChecksPassed(verifierResults)
		encoded, err := json.Marshal(verifierResults)
		if err != nil {
			return fmt.Errorf("marshal routing verifier results: %w", err)
		}
		encodedVerifier = string(encoded)
	}

	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE routing_decisions
		SET outcome_status = $1,
		    verifier_passed = COALESCE($2, verifier_passed),
		    verifier_results = CASE WHEN $2 IS NULL THEN verifier_results ELSE $3 END,
		    error = CASE WHEN $4 = '' THEN error ELSE $4 END,
		    outcome_recorded_at = $5
		WHERE agent_run_id = $6
		  AND outcome_status IN ('pending', 'paused')
	`, status, verifierPassed, encodedVerifier, outcomeErr, now, runID)
	if err != nil {
		return fmt.Errorf("record routing outcome: %w", err)
	}
	return nil
}

func (r *Runner) markRoutingHumanIntervention(ctx context.Context, runID string) error {
	if r == nil || r.db == nil || strings.TrimSpace(runID) == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE routing_decisions
		SET outcome_status = 'paused',
		    human_intervention_required = true,
		    outcome_recorded_at = $1
		WHERE agent_run_id = $2
		  AND outcome_status = 'pending'
	`, time.Now().UTC(), runID)
	if err != nil {
		return fmt.Errorf("mark routing human intervention: %w", err)
	}
	return nil
}

func finalChecksPassed(results map[string]any) bool {
	if results == nil {
		return false
	}
	if passed, ok := results["passed"].(bool); ok {
		return passed
	}

	tests, ok := results["tests"].(map[string]any)
	if !ok {
		return false
	}
	if passed, ok := tests["passed"].(bool); ok {
		return passed
	}
	output, _ := tests["output"].(string)
	if strings.TrimSpace(output) == "" {
		return false
	}
	var payload struct {
		Passed bool `json:"passed"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return false
	}
	return payload.Passed
}
