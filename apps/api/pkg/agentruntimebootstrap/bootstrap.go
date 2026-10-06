package agentruntimebootstrap

import (
	"database/sql"
	"errors"
	"fmt"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
	"github.com/ai-dev-control-plane/api/pkg/agentapproval"
	"github.com/ai-dev-control-plane/api/pkg/agentexternal"
)

// Persistence is the durable provider-neutral state required by the runtime
// manager and append-only event ledger.
type Persistence interface {
	agentruntime.Store
	agentruntime.RuntimeEventLedger
}

// Publisher is the control-plane event publisher shared by approval routing and
// external run lifecycle events.
type Publisher interface {
	Publish(subject string, data []byte) error
}

// Config describes the production wiring inputs for provider-neutral external
// agent execution. Provider creation and repository verification-plan resolution
// stay outside this package so composition remains vendor- and policy-neutral.
type Config struct {
	Database       *sql.DB
	Persistence    Persistence
	Publisher      Publisher
	CompletionGate agentexternal.CompletionGate
	Providers      []agentruntime.Provider
}

// Bundle contains the durable agent runtime manager and the external-run
// supervisor that can be attached to agentexecutor.WithExternalRunExecutor.
type Bundle struct {
	Manager    *agentruntime.Manager
	Supervisor *agentexternal.Supervisor
}

// New assembles the provider registry, durable manager, ledger-first event sink
// chain, approval bridge, and completion-gated supervisor.
func New(cfg Config) (*Bundle, error) {
	if cfg.Database == nil {
		return nil, errors.New("agent runtime bootstrap database is required")
	}
	if cfg.Persistence == nil {
		return nil, errors.New("agent runtime bootstrap persistence is required")
	}
	if cfg.Publisher == nil {
		return nil, errors.New("agent runtime bootstrap publisher is required")
	}
	if cfg.CompletionGate == nil {
		return nil, errors.New("agent runtime bootstrap completion gate is required")
	}
	if len(cfg.Providers) == 0 {
		return nil, errors.New("agent runtime bootstrap requires at least one provider")
	}

	registry := agentruntime.NewRegistry()
	for i, provider := range cfg.Providers {
		if provider == nil {
			return nil, fmt.Errorf("agent runtime bootstrap provider %d is nil", i+1)
		}
		if err := registry.Register(provider); err != nil {
			return nil, fmt.Errorf("register agent runtime provider %d: %w", i+1, err)
		}
	}

	manager, err := agentruntime.NewManager(registry, cfg.Persistence)
	if err != nil {
		return nil, fmt.Errorf("create agent runtime manager: %w", err)
	}
	ledgerSink, err := agentruntime.NewLedgerEventSink(cfg.Persistence)
	if err != nil {
		return nil, fmt.Errorf("create agent runtime ledger sink: %w", err)
	}
	approvalSink := agentapproval.NewBridge(cfg.Database, cfg.Publisher)
	sinkChain, err := agentruntime.NewEventSinkChain(ledgerSink, approvalSink)
	if err != nil {
		return nil, fmt.Errorf("create agent runtime event sink chain: %w", err)
	}
	manager.WithEventSink(sinkChain)

	supervisor := agentexternal.NewSupervisor(
		cfg.Database,
		manager,
		cfg.CompletionGate,
		cfg.Publisher,
	)
	return &Bundle{Manager: manager, Supervisor: supervisor}, nil
}
