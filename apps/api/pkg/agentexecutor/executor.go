// Package agentexecutor exposes the API agent runner to other services without
// requiring them to import API internal packages directly.
package agentexecutor

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/ai-dev-control-plane/api/internal/agentrunner"
	"github.com/ai-dev-control-plane/api/internal/audit"
	"github.com/ai-dev-control-plane/api/internal/budget"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/modelrouter"
	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/runtimes"
)

// Executor runs queued agent_runs by ID.
type Executor struct {
	runner *agentrunner.Runner
}

// New creates a production runner with the shared policy, budget, audit,
// workspace tool, model router, event, and runtime-provider wiring.
func New(db *sql.DB, eventBus *events.Bus, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	policyEngine := policies.DefaultEngine()
	budgetEngine := budget.NewEngine(db).WithLogger(logger)
	auditLogger := audit.NewLogger(db, logger)
	kernel := capability.NewKernel(policyEngine, budgetEngine, auditLogger, logger)
	runner := agentrunner.NewRunner(db, tools.NewWorkspaceTools(logger), policyEngine, budgetEngine, eventBus, logger).
		WithCapabilityKernel(kernel)
	return &Executor{runner: runner}
}

// WithRuntimeProvider registers the runtime provider used by queued runs.
func (e *Executor) WithRuntimeProvider(name string, provider runtimes.Provider) *Executor {
	if e == nil || e.runner == nil || provider == nil || name == "" {
		return e
	}
	e.runner.WithRuntimeProvider(name, provider)
	return e
}

// WithPolicyEngine replaces the policy engine used by the runner's capability
// kernel. This is intended for integration tests that need to relax approval
// requirements without modifying the production default policy set.
func (e *Executor) WithPolicyEngine(engine *policies.Engine) *Executor {
	if e == nil || e.runner == nil || engine == nil {
		return e
	}
	e.runner.WithPolicyEngine(engine)
	return e
}

// WithDeterministicResponses replaces the model router with one that returns
// fixed responses in order. Each response must be a JSON-encoded model action
// (tool_call, final_response, handoff, or request_approval). Intended for
// integration tests that need to drive the agent loop without calling a live
// model provider.
func (e *Executor) WithDeterministicResponses(responses ...string) *Executor {
	if e == nil || e.runner == nil || len(responses) == 0 {
		return e
	}
	router := modelrouter.NewRouter(nil, &deterministicProvider{responses: responses})
	e.runner.WithModelRouter(router)
	return e
}

// ExecuteRun executes the agent run identified by runID.
func (e *Executor) ExecuteRun(ctx context.Context, runID string) error {
	return e.runner.Run(ctx, runID)
}

// deterministicProvider is a modelrouter.Provider that returns pre-configured
// responses in order. It is only used by WithDeterministicResponses.
type deterministicProvider struct {
	responses []string
	index     int
}

func (p *deterministicProvider) Name() string { return "deterministic" }

func (p *deterministicProvider) Models() []modelrouter.ModelInfo {
	return []modelrouter.ModelInfo{{
		Name:                     "deterministic",
		Provider:                 "deterministic",
		SupportsStructuredOutput: true,
		SupportsFunctionCalling:  true,
	}}
}

func (p *deterministicProvider) IsAvailable() bool { return true }

func (p *deterministicProvider) Call(ctx context.Context, req modelrouter.CallRequest) (*modelrouter.CallResult, error) {
	if p.index >= len(p.responses) {
		return nil, errors.New("deterministic provider exhausted: no more responses")
	}
	resp := p.responses[p.index]
	p.index++
	return &modelrouter.CallResult{
		Content:          resp,
		Model:            "deterministic",
		Provider:         "deterministic",
		PromptTokens:     len(req.Messages),
		CompletionTokens: len(resp),
		TotalTokens:      len(req.Messages) + len(resp),
	}, nil
}
