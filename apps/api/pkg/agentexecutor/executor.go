// Package agentexecutor exposes the API agent runner to other services without
// requiring them to import API internal packages directly.
package agentexecutor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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

const (
	executionBackendLegacy       = "legacy"
	executionBackendAgentRuntime = "agent_runtime"
)

type legacyRunBackend interface {
	Run(ctx context.Context, runID string) error
}

// ExternalRunExecutor executes runs selected for the provider-neutral agent runtime.
type ExternalRunExecutor interface {
	ExecuteExternalRun(ctx context.Context, runID string) error
}

// Executor runs queued agent_runs by ID.
type Executor struct {
	db       *sql.DB
	runner   *agentrunner.Runner
	legacy   legacyRunBackend
	external ExternalRunExecutor
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
	return &Executor{db: db, runner: runner, legacy: runner}
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

// WithExternalRunExecutor configures the provider-neutral external-agent backend.
// Runs remain on the legacy model-driven runner unless metadata explicitly selects it.
func (e *Executor) WithExternalRunExecutor(external ExternalRunExecutor) *Executor {
	if e == nil {
		return e
	}
	e.external = external
	return e
}

// ExecuteRun executes the agent run identified by runID using the durable
// execution-backend selection stored in agent_runs.metadata.
func (e *Executor) ExecuteRun(ctx context.Context, runID string) error {
	if e == nil || e.db == nil {
		return errors.New("agent executor database is not configured")
	}
	if strings.TrimSpace(runID) == "" {
		return errors.New("agent run id is required")
	}

	var metadataRaw string
	if err := e.db.QueryRowContext(ctx, `
		SELECT COALESCE(metadata, '{}')
		FROM agent_runs
		WHERE id = $1
	`, runID).Scan(&metadataRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("agent run %s not found", runID)
		}
		return fmt.Errorf("load agent run execution metadata: %w", err)
	}

	backend, err := executionBackend(metadataRaw)
	if err != nil {
		return err
	}
	switch backend {
	case executionBackendLegacy:
		if e.legacy == nil {
			return errors.New("legacy agent runner is not configured")
		}
		return e.legacy.Run(ctx, runID)
	case executionBackendAgentRuntime:
		if e.external == nil {
			return errors.New("agent runtime executor is not configured")
		}
		return e.external.ExecuteExternalRun(ctx, runID)
	default:
		return fmt.Errorf("unsupported execution backend %q", backend)
	}
}

func executionBackend(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return executionBackendLegacy, nil
	}
	var metadata struct {
		Execution struct {
			Backend string `json:"backend"`
		} `json:"execution"`
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return "", fmt.Errorf("decode execution metadata: %w", err)
	}
	backend := strings.ToLower(strings.TrimSpace(metadata.Execution.Backend))
	if backend == "" {
		return executionBackendLegacy, nil
	}
	return backend, nil
}

// CheckRunStart evaluates budget admission before a queued run enters the running set.
func (e *Executor) CheckRunStart(ctx context.Context, runID string) (bool, string, error) {
	if e == nil || e.runner == nil {
		return false, "", errors.New("agent executor is not configured")
	}
	result, err := e.runner.CheckRunStart(ctx, runID)
	if err != nil {
		return false, "", err
	}
	if result == nil {
		return false, "budget check returned no result", nil
	}
	return result.Allowed, result.Reason, nil
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
