package handlers

import (
	"context"
	"database/sql"
	"log/slog"

	agentvaultclient "github.com/ai-dev-control-plane/api/internal/agentvault"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/secrets"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/runtimes"
	"golang.org/x/oauth2"
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
	githubGateway     githubGateway
	githubToken       string
	deployGateway     deployGateway
	deployToken       string
}

// githubGateway is the subset of the GitHub gateway used by handlers.
type githubGateway interface {
	MergePR(ctx context.Context, token *oauth2.Token, owner, name string, number int, req gateway.MergePRRequest) (*gateway.MergePRResult, error)
}

// deployGateway is the subset of a deployment provider used by handlers.
type deployGateway interface {
	CreateDeployment(ctx context.Context, token *oauth2.Token, owner, name, environment, ref string) (*gateway.Deployment, error)
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

// WithGitHubGateway injects a GitHub gateway for PR/merge operations.
func (h *Handler) WithGitHubGateway(g githubGateway) *Handler {
	h.githubGateway = g
	return h
}

// WithGitHubToken configures the GitHub token used for PR/merge operations.
func (h *Handler) WithGitHubToken(token string) *Handler {
	h.githubToken = token
	return h
}

// WithDeployGateway injects a deployment gateway for task deploy operations.
func (h *Handler) WithDeployGateway(g deployGateway) *Handler {
	h.deployGateway = g
	return h
}

// WithDeployToken configures the deployment token used for task deploy operations.
func (h *Handler) WithDeployToken(token string) *Handler {
	h.deployToken = token
	return h
}

func (h *Handler) kernel() *capability.Kernel {
	if h.capabilityKernel == nil {
		h.capabilityKernel = capability.NewKernel(nil, nil, nil, h.logger)
	}
	return h.capabilityKernel
}
