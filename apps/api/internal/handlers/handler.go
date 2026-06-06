package handlers

import (
	"database/sql"
	"log/slog"

	agentvaultclient "github.com/ai-dev-control-plane/api/internal/agentvault"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/secrets"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/runtimes"
)

// EventPublisher is the subset of the event bus used by HTTP handlers.
type EventPublisher interface {
	Publish(subject string, data []byte) error
}

// Handler is the base handler struct that provides access to shared dependencies.
type Handler struct {
	db                *sql.DB
	logger            *slog.Logger
	eventBus          EventPublisher
	agentVault        *agentvaultclient.Client
	agentVaultProject string
	capabilityKernel  *capability.Kernel
	runtimeProviders  map[string]runtimes.Provider
	secretManager     *secrets.Manager
}

// NewHandler creates a new base handler with the given dependencies.
func NewHandler(db *sql.DB, logger *slog.Logger) *Handler {
	return &Handler{db: db, logger: logger}
}

// WithEventBus adds a NATS event bus to the handler for publishing events.
// Call this after NewHandler to enable event publishing from handlers.
func (h *Handler) WithEventBus(bus *events.Bus) *Handler {
	h.eventBus = bus
	return h
}

// WithEventPublisher adds a publisher implementation, primarily for tests.
func (h *Handler) WithEventPublisher(pub EventPublisher) *Handler {
	h.eventBus = pub
	return h
}

// WithAgentVault enables optional durable event logging to AgentVault.
func (h *Handler) WithAgentVault(client *agentvaultclient.Client, project string) *Handler {
	h.agentVault = client
	h.agentVaultProject = project
	return h
}

// WithCapabilityKernel sets the kernel used to authorize dangerous operations.
func (h *Handler) WithCapabilityKernel(kernel *capability.Kernel) *Handler {
	h.capabilityKernel = kernel
	return h
}

// WithRuntimeProvider registers a workspace runtime provider for workspace
// file, patch, and exec operations.
func (h *Handler) WithRuntimeProvider(name string, provider runtimes.Provider) *Handler {
	if h.runtimeProviders == nil {
		h.runtimeProviders = map[string]runtimes.Provider{}
	}
	h.runtimeProviders[name] = provider
	return h
}

// WithSecretManager enables encrypted secret storage endpoints.
func (h *Handler) WithSecretManager(manager *secrets.Manager) *Handler {
	h.secretManager = manager
	return h
}

func (h *Handler) kernel() *capability.Kernel {
	if h.capabilityKernel == nil {
		h.capabilityKernel = capability.NewKernel(nil, nil, nil, h.logger)
	}
	return h.capabilityKernel
}
