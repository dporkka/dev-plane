// Package server provides the HTTP server setup for the API service.
package server

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	agentvaultclient "github.com/ai-dev-control-plane/api/internal/agentvault"
	"github.com/ai-dev-control-plane/api/internal/audit"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/config"
	"github.com/ai-dev-control-plane/api/internal/handlers"
	appmiddleware "github.com/ai-dev-control-plane/api/internal/middleware"
	"github.com/ai-dev-control-plane/api/internal/openapi"
	"github.com/ai-dev-control-plane/api/internal/otel"
	"github.com/ai-dev-control-plane/api/internal/secrets"
	events "github.com/ai-dev-control-plane/events"
)

// Server is the HTTP server for the API service.
type Server struct {
	router   chi.Router
	db       *sql.DB
	logger   *slog.Logger
	config   *config.Config
	eventBus *events.Bus
	httpSrv  *http.Server
}

// New creates a new Server with all routes and middleware configured.
func New(cfg *config.Config, database *sql.DB, logger *slog.Logger) *Server {
	s := &Server{
		router: chi.NewRouter(),
		db:     database,
		logger: logger,
		config: cfg,
	}

	// Attempt to connect to NATS for event publishing
	if cfg.NATSURL != "" {
		bus, err := events.New(cfg.NATSURL)
		if err != nil {
			logger.Warn("failed to connect to NATS, event publishing disabled", "error", err)
		} else {
			if err := bus.CreateStreams(); err != nil {
				logger.Warn("failed to create NATS streams, event publishing disabled", "error", err)
			} else {
				s.eventBus = bus
				logger.Info("NATS event bus connected", "url", cfg.NATSURL)
			}
		}
	}

	s.routes()
	return s
}

func (s *Server) routes() {
	// OpenTelemetry HTTP tracing middleware (first to capture full request lifecycle)
	s.router.Use(otel.Middleware)

	// Built-in Chi middleware
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)

	// Custom middleware
	s.router.Use(appmiddleware.Logger(s.logger))
	s.router.Use(appmiddleware.Recovery)
	s.router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   s.config.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-ID"},
		ExposedHeaders:   []string{"Link", "X-Trace-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// OpenAPI documentation (public)
	s.router.Get("/api/docs", openapi.DocsHandler)
	s.router.Get("/api/openapi.json", openapi.SpecHandler)

	// Initialize handlers
	auditLogger := audit.NewLogger(s.db, s.logger)
	capabilityKernel := capability.NewKernel(nil, nil, auditLogger, s.logger)
	h := handlers.NewHandler(s.db, s.logger).WithCapabilityKernel(capabilityKernel)
	if s.config.SecretKeys != "" {
		keyring, err := secrets.ParseKeyring(s.config.SecretKeys)
		if err != nil {
			s.logger.Error("invalid secret encryption key configuration", "error", err)
		} else {
			h = h.WithSecretManager(secrets.NewManager(s.db, keyring, auditLogger, s.logger))
		}
	} else {
		s.logger.Warn("SECRET_ENCRYPTION_KEYS not configured; encrypted secret storage endpoints are disabled")
	}
	if s.eventBus != nil {
		h = h.WithEventBus(s.eventBus)
	}
	if av := agentvaultclient.NewClient(s.config.AgentVaultURL, s.config.AgentVaultToken); av != nil {
		h = h.WithAgentVault(av, s.config.AgentVaultProject)
	}
	ghAuth := handlers.NewGitHubAuthHandler(s.db, s.config)
	wh := handlers.NewWebhookHandler().WithWebhookSecret(s.config.GitHubWebhookSecret)
	if s.eventBus != nil {
		wh = wh.WithEventPublisher(s.eventBus)
	}

	// Health checks (public)
	s.router.Get("/health", handlers.HealthCheck)
	s.router.Get("/ready", handlers.ReadyCheck)

	// API v1
	s.router.Route("/api/v1", func(r chi.Router) {
		// Public auth endpoints
		r.Get("/auth/github", ghAuth.GitHubAuthRedirect)
		r.Get("/auth/github/callback", ghAuth.GitHubAuthCallback)

		// GitHub webhooks (public but signed)
		r.Post("/webhooks/github", wh.GitHubWebhook)
		r.Post("/webhooks/linear", wh.LinearWebhook)
		r.Post("/webhooks/slack", wh.SlackWebhook)
		r.Post("/webhooks/discord", wh.DiscordWebhook)

		// Authenticated endpoints
		r.Group(func(r chi.Router) {
			r.Use(appmiddleware.Auth(s.config.JWTSecret))

			// Organizations
			r.Get("/organizations", h.ListOrganizations)
			r.Post("/organizations", h.CreateOrganization)
			r.Get("/organizations/{id}", h.GetOrganization)

			// Projects
			r.Get("/organizations/{orgID}/projects", h.ListProjects)
			r.Post("/organizations/{orgID}/projects", h.CreateProject)
			r.Get("/projects/{id}", h.GetProject)

			// Repositories
			r.Get("/projects/{projectID}/repositories", h.ListRepositories)
			r.Post("/projects/{projectID}/repositories", h.ConnectRepository)
			r.Get("/repositories/{id}", h.GetRepository)
			r.Delete("/repositories/{id}", h.DisconnectRepository)
			r.Post("/repositories/{id}/sync", h.SyncRepository)

			// Tasks
			r.Get("/projects/{projectID}/tasks", h.ListTasks)
			r.Post("/projects/{projectID}/tasks", h.CreateTask)
			r.Post("/projects/{projectID}/brief-handoffs", h.CreateBriefHandoff)
			r.Get("/tasks/{id}", h.GetTask)
			r.Patch("/tasks/{id}", h.UpdateTask)
			r.Post("/tasks/{id}/approve-spec", h.ApproveSpec)
			r.Post("/tasks/{id}/cancel", h.CancelTask)

			// Task actions
			r.Post("/tasks/{id}/generate-spec", h.GenerateSpec)
			r.Post("/tasks/{id}/start-run", h.StartRun)
			r.Post("/runs/{id}/retry", h.RetryRun)
			r.Get("/runs/{id}/events", h.GetRunEvents)

			// Agent Runs
			r.Get("/tasks/{taskID}/runs", h.ListAgentRuns)
			r.Get("/runs/{id}", h.GetAgentRun)
			r.Get("/runs/{id}/steps", h.ListAgentSteps)
			r.Post("/runs/{id}/cancel", h.CancelAgentRun)
			r.Get("/runs/{id}/stream", h.StreamAgentRun)

			// Reviews
			r.Get("/runs/{runId}/review", h.GetReview)
			r.Post("/runs/{runId}/review", h.RequestReview)

			// Approvals
			r.Get("/tasks/{taskID}/approvals", h.ListApprovals)
			r.Get("/organizations/{orgID}/approvals", h.ListOrganizationApprovals)
			r.Post("/approvals/{id}/respond", h.RespondApproval)

			// Pull Requests
			r.Get("/projects/{projectID}/pull-requests", h.ListPullRequests)
			r.Get("/pull-requests/{id}", h.GetPullRequest)
			r.Post("/tasks/{taskId}/pull-request", h.CreatePullRequest)
			r.Post("/pull-requests/{id}/merge", h.MergePullRequest)

			// Deployments
			r.Post("/tasks/{id}/deploy", h.DeployTask)

			// Repositories
			r.Post("/repositories/{id}/analyze", h.AnalyzeRepo)

			// Workspaces
			r.Get("/tasks/{taskID}/workspaces", h.ListTaskWorkspaces)
			r.Get("/workspaces/{id}", h.GetWorkspace)
			r.Post("/workspaces/{id}/destroy", h.DestroyWorkspace)
			r.Get("/workspaces/{id}/diff", h.GetWorkspaceDiff)

			// Workspace file operations
			r.Get("/workspaces/{id}/files", h.ListWorkspaceFiles)
			r.Get("/workspaces/{id}/files/content", h.ReadWorkspaceFile)
			r.Post("/workspaces/{id}/files/write", h.WriteWorkspaceFile)
			r.Post("/workspaces/{id}/patch", h.ApplyWorkspacePatch)
			r.Post("/workspaces/{id}/exec", h.ExecWorkspaceCommand)
			r.Post("/workspaces/{id}/start-service", h.StartWorkspaceService)
			r.Post("/workspaces/{id}/stop-service", h.StopWorkspaceService)

			// Artifacts
			r.Get("/artifacts/{id}", h.GetArtifact)

			// Policies
			r.Get("/organizations/{orgID}/policies", h.ListPolicies)
			r.Post("/organizations/{orgID}/policies", h.CreatePolicy)

			// Audit Logs
			r.Get("/organizations/{orgID}/audit-logs", h.ListAuditLogs)

			// Secrets
			r.Get("/organizations/{orgID}/secrets", h.ListSecrets)
			r.Post("/organizations/{orgID}/secrets", h.CreateSecret)
			r.Post("/secrets/{id}/rotate", h.RotateSecret)

			// Dashboard
			r.Get("/organizations/{orgID}/dashboard", h.GetDashboard)

			// Integrations
			r.Get("/organizations/{orgID}/integrations", h.ListIntegrations)
			r.Post("/organizations/{orgID}/integrations", h.CreateIntegration)
			r.Patch("/integrations/{id}", h.UpdateIntegration)
			r.Delete("/integrations/{id}", h.DeleteIntegration)
		})
	})
}

// Start starts the HTTP server on the given address.
func (s *Server) Start(addr string) error {
	s.logger.Info("starting server", "addr", addr)
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops the HTTP server and closes dependencies.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down server")
	if s.httpSrv != nil {
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			return err
		}
	}
	if s.eventBus != nil {
		return s.eventBus.Close()
	}
	return nil
}

// Handler returns the HTTP handler for testing.
func (s *Server) Handler() http.Handler {
	return s.router
}

// Close gracefully shuts down the server and its dependencies.
func (s *Server) Close() error {
	if s.httpSrv != nil {
		_ = s.httpSrv.Close()
	}
	if s.eventBus != nil {
		return s.eventBus.Close()
	}
	return nil
}
