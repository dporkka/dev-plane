// Package main implements the worker service for the AI Dev Control Plane.
//
// The worker connects to NATS JetStream, creates durable consumers for each
// event type, and dispatches messages to appropriate handlers. It handles
// graceful shutdown on SIGINT/SIGTERM.
//
// Event flow:
//
//	tasks.created        -> trigger spec generation
//	tasks.approved       -> create workspace + start agent run
//	agents.run.completed  -> consume mailbox handoffs or review completed work
//	agents.run.failed     -> transition task to failed and notify integrations
//	review.completed     -> request human PR approval
//	approval.approved    -> create PR if type=pr_create
//	approval.rejected    -> update task status, notify user
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/api/pkg/agentexecutor"
	"github.com/ai-dev-control-plane/crypto"
	"github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/reviewer"
	"github.com/ai-dev-control-plane/runtimes"

	"github.com/ai-dev-control-plane/worker/internal/handlers"
	"github.com/ai-dev-control-plane/worker/internal/webhooks"
)

func main() {
	os.Exit(runWorker())
}

// runWorker returns an exit code so connection and shutdown defers always run.
func runWorker() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Parse flags
	var (
		dbURL                = flag.String("db", os.Getenv("DATABASE_URL"), "Database URL (or DATABASE_URL env)")
		natsURL              = flag.String("nats", os.Getenv("NATS_URL"), "NATS URL (or NATS_URL env, default nats://localhost:4222)")
		logLevel             = flag.String("log-level", os.Getenv("LOG_LEVEL"), "Log level (debug, info, warn, error)")
		runtimeName          = flag.String("workspace-runtime", os.Getenv("WORKSPACE_RUNTIME"), "Workspace runtime: local, docker, or remote")
		runtimeBaseDir       = flag.String("workspace-base-dir", os.Getenv("WORKSPACE_BASE_DIR"), "Workspace runtime base directory")
		schedulerMaxParallel = flag.Int("scheduler-max-parallel", envIntOrDefault("SCHEDULER_MAX_PARALLEL", 2), "Maximum concurrently admitted scheduler-enabled runs per worker")
		schedulerCPU         = flag.Float64("scheduler-cpu", envFloatOrDefault("SCHEDULER_CPU", 4), "Scheduler CPU capacity units")
		schedulerMemoryMB    = flag.Int("scheduler-memory-mb", envIntOrDefault("SCHEDULER_MEMORY_MB", 8192), "Scheduler memory capacity in MiB")
		runLeaseEnabled      = flag.Bool("run-lease-enabled", os.Getenv("DEV_PLANE_RUN_LEASE_ENABLED") == "1", "Enable opt-in run execution lease liveness (requires migration 025)")
	)
	flag.Parse()

	if *dbURL == "" {
		*dbURL = "file:/tmp/ai-dev-control-plane.db"
		logger.Warn("no database URL provided, using default SQLite", "path", *dbURL)
	}
	if *natsURL == "" {
		*natsURL = "nats://localhost:4222"
	}
	if *runtimeName == "" {
		*runtimeName = "local"
	}
	if *runtimeBaseDir == "" {
		*runtimeBaseDir = filepath.Join(os.TempDir(), "ai-dev-control-plane-workspaces")
	}

	workerLane, consumeWebhooks, err := workerRoutingConfig(*runtimeName)
	if err != nil {
		logger.Error("invalid worker routing configuration", "error", err)
		return 1
	}

	// Set log level
	if *logLevel != "" {
		var level slog.Level
		if err := level.UnmarshalText([]byte(*logLevel)); err == nil {
			logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
				Level: level,
			}))
		}
	}

	logger.Info("starting worker service",
		"worker_lane", workerLane,
		"db_driver", detectDriver(*dbURL),
		"workspace_runtime", *runtimeName,
	)

	// Set up graceful shutdown context.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Connect to database
	database, err := db.New(*dbURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		return 1
	}
	defer func() {
		if err := database.Close(); err != nil {
			logger.Error("database close failed", "error", err)
		}
	}()

	if err := database.Ping(ctx); err != nil {
		logger.Error("database ping failed", "error", err)
		return 1
	}
	logger.Info("database connected")

	// Connect to NATS
	eventBus, err := events.New(*natsURL)
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		return 1
	}
	defer func() {
		if err := eventBus.Close(); err != nil {
			logger.Error("event bus close failed", "error", err)
		}
	}()

	if err := eventBus.CreateStreams(); err != nil {
		logger.Error("failed to create NATS streams", "error", err)
		return 1
	}
	eventBus.WithExecutionRouting(database.DB)
	logger.Info("NATS event bus connected and streams created")

	var subscriptions workerHandlers
	if workerLane != laneWebhooks {
		runtimeProvider, runtimeProviderName, err := initRuntimeProvider(*runtimeName, *runtimeBaseDir, os.Getenv("RUNNER_URL"), os.Getenv("RUNNER_AUTH_TOKEN"))
		if err != nil {
			logger.Error("failed to initialize workspace runtime", "error", err)
			return 1
		}
		logger.Info("workspace runtime initialized", "runtime", runtimeProviderName, "base_dir", *runtimeBaseDir)

		var keyring *crypto.Keyring
		if keysRaw := os.Getenv("SECRET_ENCRYPTION_KEYS"); keysRaw != "" {
			keyring, err = crypto.ParseKeyring(keysRaw)
			if err != nil {
				logger.Error("invalid SECRET_ENCRYPTION_KEYS", "error", err)
				return 1
			}
			logger.Info("integration token keyring configured")
		} else {
			logger.Warn("SECRET_ENCRYPTION_KEYS not configured; encrypted integration tokens cannot be decrypted")
		}

		// Create handlers
		taskHandler := handlers.NewTaskHandler(database.DB, logger).WithEventPublisher(eventBus).WithRuntimeProvider(runtimeProvider, runtimeProviderName)
		runExecutor := agentexecutor.New(database.DB, eventBus, logger).WithRuntimeProvider(runtimeProviderName, runtimeProvider)
		reviewService := reviewer.NewReviewer(database.DB, logger)
		runAdmission := handlers.NewSchedulerAdmission(database.DB, handlers.SchedulerCapacity{
			MaxParallel: *schedulerMaxParallel,
			CPU:         *schedulerCPU,
			MemoryMB:    *schedulerMemoryMB,
		}).WithStartBudget(runExecutor)
		runHandler := handlers.NewRunHandler(database.DB, logger, eventBus).
			WithRunExecutor(runExecutor).
			WithRunAdmission(runAdmission).
			WithReviewer(reviewService)
		if *runLeaseEnabled {
			lease := handlers.NewRunLeaseManager(database.DB, logger)
			if err := lease.CheckSchema(ctx); err != nil {
				logger.Error("run lease enabled before schema migration", "error", err)
				return 1
			}
			runHandler.WithRunLease(lease)
			go lease.RunReviewSweep(ctx, time.Minute)
			logger.Info("run liveness leases enabled (expired runs flagged for manual review; no replay)")
		}
		approvalHandler := handlers.NewApprovalHandler(database.DB, logger, eventBus)
		notificationHandler := handlers.NewNotificationHandler(database.DB, logger, eventBus).WithKeyring(keyring)

		subscriptions = workerHandlers{
			taskCreated:       taskHandler.HandleTaskCreated,
			taskApproved:      taskHandler.HandleTaskApproved,
			runCompleted:      runHandler.HandleRunCompleted,
			runFailed:         runHandler.HandleRunFailed,
			reviewCompleted:   runHandler.HandleReviewCompleted,
			approvalApproved:  approvalHandler.HandleApprovalApproved,
			approvalRejected:  approvalHandler.HandleApprovalRejected,
			approvalRequested: notificationHandler.HandleApprovalRequested,
			taskCompleted:     notificationHandler.HandleTaskCompleted,
			taskFailed:        notificationHandler.HandleTaskFailed,
			runTriggered:      runHandler.HandleRunTriggered,
		}
	}
	if consumeWebhooks {
		webhookConsumer := webhooks.NewConsumer(database.DB, logger, eventBus).WithGiteaWebhook(os.Getenv("GITEA_URL"), os.Getenv("GITEA_WEBHOOK_SECRET"))
		subscriptions.webhook = webhookConsumer.Handle
	}
	shutdownCtx := &shutdownContext{logger: logger}
	if err := registerWorkerSubscriptions(database.DB, eventBus, workerLane, subscriptions, shutdownCtx); err != nil {
		logger.Error("failed to subscribe worker lane", "error", err)
		return 1
	}

	// Start the minimal health HTTP server used by container health checks.
	healthPort := envOrDefault("WORKER_HEALTH_PORT", "8081")
	healthAddr, stopHealth := startHealthServer(ctx, healthPort, logger)
	if healthAddr == "" {
		logger.Error("failed to start worker health server", "port", healthPort)
		return 1
	}
	logger.Info("worker health server started", "addr", healthAddr)

	logger.Info("worker service is running, waiting for events...")

	// Wait for shutdown signal
	<-ctx.Done()

	logger.Info("shutdown signal received")
	shutdownCtx.shutdown()
	stopHealth()
	logger.Info("worker service stopped gracefully")
	return 0
}

// shutdownContext tracks subscriptions for graceful shutdown.
type shutdownContext struct {
	subscriptions []*nats.Subscription
	logger        *slog.Logger
}

func (c *shutdownContext) addSubscription(sub *nats.Subscription) {
	c.subscriptions = append(c.subscriptions, sub)
}

func (c *shutdownContext) shutdown() {
	c.logger.Info("unsubscribing from all subjects")
	for _, sub := range c.subscriptions {
		if err := sub.Unsubscribe(); err != nil {
			c.logger.Warn("failed to unsubscribe", "error", err)
		}
	}
	// Give in-flight handlers time to complete
	time.Sleep(500 * time.Millisecond)
}

// detectDriver returns the database driver name from the URL.
func detectDriver(url string) string {
	if len(url) > 5 && url[:5] == "file:" {
		return "sqlite"
	}
	if len(url) >= 8 && url[:8] == "postgres" {
		return "postgres"
	}
	return "unknown"
}

func initRuntimeProvider(name, baseDir, runnerURL, runnerToken string) (runtimes.Provider, string, error) {
	if runnerURL != "" {
		return runtimes.NewRemoteProvider(runnerURL, runnerToken), "remote", nil
	}
	if isProduction() && name == "local" {
		return nil, "", errors.New("local workspace runtime is not allowed in production")
	}
	return runtimes.NewProvider(name, baseDir, runnerURL, runnerToken)
}

func isProduction() bool {
	return os.Getenv("APP_ENV") == "production" || os.Getenv("ENV") == "production"
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func envIntOrDefault(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

func envFloatOrDefault(key string, defaultValue float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

// startHealthServer starts a minimal HTTP server for container health checks.
// It listens on the configured port and responds to GET /health with a JSON
// status payload. The server is shut down when the provided context is canceled.
// It returns the listener address and a function that can be called to wait for
// graceful shutdown.
func startHealthServer(ctx context.Context, port string, logger *slog.Logger) (string, func()) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})

	listener, err := net.Listen("tcp", net.JoinHostPort("", port))
	if err != nil {
		if logger != nil {
			logger.Error("failed to start health server", "error", err)
		}
		return "", func() {}
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("health server error", "error", err)
		}
	}()

	stop := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("health server shutdown error", "error", err)
		}
		_ = listener.Close()
	}

	return listener.Addr().String(), stop
}
