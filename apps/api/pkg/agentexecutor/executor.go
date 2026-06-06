// Package agentexecutor exposes the API agent runner to other services without
// requiring them to import API internal packages directly.
package agentexecutor

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/ai-dev-control-plane/api/internal/agentrunner"
	"github.com/ai-dev-control-plane/api/internal/audit"
	"github.com/ai-dev-control-plane/api/internal/budget"
	"github.com/ai-dev-control-plane/api/internal/capability"
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

// ExecuteRun executes the agent run identified by runID.
func (e *Executor) ExecuteRun(ctx context.Context, runID string) error {
	return e.runner.Run(ctx, runID)
}
