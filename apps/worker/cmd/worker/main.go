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
//	review.completed     -> request human PR approval
//	approval.approved    -> create PR if type=pr_create
//	approval.rejected    -> update task status, notify user
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/api/pkg/agentexecutor"
	"github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/reviewer"
	"github.com/ai-dev-control-plane/runtimes"

	"github.com/ai-dev-control-plane/worker/internal/handlers"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Parse flags
	var (
		dbURL          = flag.String("db", os.Getenv("DATABASE_URL"), "Database URL (or DATABASE_URL env)")
		natsURL        = flag.String("nats", os.Getenv("NATS_URL"), "NATS URL (or NATS_URL env, default nats://localhost:4222)")
		logLevel       = flag.String("log-level", os.Getenv("LOG_LEVEL"), "Log level (debug, info, warn, error)")
		runtimeName    = flag.String("workspace-runtime", os.Getenv("WORKSPACE_RUNTIME"), "Workspace runtime: local or docker")
		runtimeBaseDir = flag.String("workspace-base-dir", os.Getenv("WORKSPACE_BASE_DIR"), "Workspace runtime base directory")
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
		"nats_url", *natsURL,
		"db_driver", detectDriver(*dbURL),
		"workspace_runtime", *runtimeName,
	)

	// Connect to database
	database, err := db.New(*dbURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := database.Ping(context.Background()); err != nil {
		logger.Error("database ping failed", "error", err)
		os.Exit(1)
	}
	logger.Info("database connected")

	// Connect to NATS
	eventBus, err := events.New(*natsURL)
	if err != nil {
		logger.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer eventBus.Close()

	if err := eventBus.CreateStreams(); err != nil {
		logger.Error("failed to create NATS streams", "error", err)
		os.Exit(1)
	}
	logger.Info("NATS event bus connected and streams created")

	runtimeProvider, runtimeProviderName, err := newRuntimeProvider(*runtimeName, *runtimeBaseDir)
	if err != nil {
		logger.Error("failed to initialize workspace runtime", "error", err)
		os.Exit(1)
	}
	logger.Info("workspace runtime initialized", "runtime", runtimeProviderName, "base_dir", *runtimeBaseDir)

	// Create handlers
	taskHandler := handlers.NewTaskHandler(database.DB, logger).WithEventPublisher(eventBus).WithRuntimeProvider(runtimeProvider, runtimeProviderName)
	runExecutor := agentexecutor.New(database.DB, eventBus, logger).WithRuntimeProvider(runtimeProviderName, runtimeProvider)
	reviewService := reviewer.NewReviewer(database.DB, logger)
	runHandler := handlers.NewRunHandler(database.DB, logger, eventBus).WithRunExecutor(runExecutor).WithReviewer(reviewService)
	approvalHandler := handlers.NewApprovalHandler(database.DB, logger, eventBus)

	// Set up subscriptions
	ctx := &shutdownContext{logger: logger}

	// tasks.created -> spec generation
	subTaskCreated, err := eventBus.Subscribe("tasks.created", func(msg *nats.Msg) {
		logger.Debug("received tasks.created event")
		if err := taskHandler.HandleTaskCreated(msg); err != nil {
			logger.Error("failed to handle task created", "error", err)
		}
	})
	if err != nil {
		logger.Error("failed to subscribe to tasks.created", "error", err)
		os.Exit(1)
	}
	ctx.addSubscription(subTaskCreated)
	logger.Info("subscribed to tasks.created")

	// tasks.approved -> workspace + agent run
	subTaskApproved, err := eventBus.Subscribe("tasks.approved", func(msg *nats.Msg) {
		logger.Debug("received tasks.approved event")
		if err := taskHandler.HandleTaskApproved(msg); err != nil {
			logger.Error("failed to handle task approved", "error", err)
		}
	})
	if err != nil {
		logger.Error("failed to subscribe to tasks.approved", "error", err)
		os.Exit(1)
	}
	ctx.addSubscription(subTaskApproved)
	logger.Info("subscribed to tasks.approved")

	// agents.run.completed -> mailbox handoff scheduling or review generation
	subRunCompleted, err := eventBus.Subscribe("agents.run.completed", func(msg *nats.Msg) {
		logger.Debug("received agents.run.completed event")
		if err := runHandler.HandleRunCompleted(msg); err != nil {
			logger.Error("failed to handle run completed", "error", err)
		}
	})
	if err != nil {
		logger.Error("failed to subscribe to agents.run.completed", "error", err)
		os.Exit(1)
	}
	ctx.addSubscription(subRunCompleted)
	logger.Info("subscribed to agents.run.completed")

	// review.completed -> PR approval request
	subReviewCompleted, err := eventBus.Subscribe("review.completed", func(msg *nats.Msg) {
		logger.Debug("received review.completed event")
		if err := runHandler.HandleReviewCompleted(msg); err != nil {
			logger.Error("failed to handle review completed", "error", err)
		}
	})
	if err != nil {
		logger.Error("failed to subscribe to review.completed", "error", err)
		os.Exit(1)
	}
	ctx.addSubscription(subReviewCompleted)
	logger.Info("subscribed to review.completed")

	// approval.approved -> create PR
	subApprovalApproved, err := eventBus.Subscribe("approval.approved", func(msg *nats.Msg) {
		logger.Debug("received approval.approved event")
		if err := approvalHandler.HandleApprovalApproved(msg); err != nil {
			logger.Error("failed to handle approval approved", "error", err)
		}
	})
	if err != nil {
		logger.Error("failed to subscribe to approval.approved", "error", err)
		os.Exit(1)
	}
	ctx.addSubscription(subApprovalApproved)
	logger.Info("subscribed to approval.approved")

	// approval.rejected -> fail task
	subApprovalRejected, err := eventBus.Subscribe("approval.rejected", func(msg *nats.Msg) {
		logger.Debug("received approval.rejected event")
		if err := approvalHandler.HandleApprovalRejected(msg); err != nil {
			logger.Error("failed to handle approval rejected", "error", err)
		}
	})
	if err != nil {
		logger.Error("failed to subscribe to approval.rejected", "error", err)
		os.Exit(1)
	}
	ctx.addSubscription(subApprovalRejected)
	logger.Info("subscribed to approval.rejected")

	// Also subscribe to runs.triggered for agent execution
	subRunTriggered, err := eventBus.Subscribe("runs.triggered", func(msg *nats.Msg) {
		logger.Debug("received runs.triggered event", "data", string(msg.Data))
		if err := runHandler.HandleRunTriggered(msg); err != nil {
			logger.Error("failed to handle run triggered", "error", err)
		}
	})
	if err != nil {
		logger.Error("failed to subscribe to runs.triggered", "error", err)
		os.Exit(1)
	}
	ctx.addSubscription(subRunTriggered)
	logger.Info("subscribed to runs.triggered")

	logger.Info("worker service is running, waiting for events...")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan

	logger.Info("shutdown signal received", "signal", sig.String())
	ctx.shutdown()
	logger.Info("worker service stopped gracefully")
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

func newRuntimeProvider(name, baseDir string) (runtimes.Provider, string, error) {
	switch strings.ToLower(name) {
	case "local":
		return runtimes.NewLocalProvider(baseDir), "local", nil
	case "docker":
		provider, err := runtimes.NewDockerProvider(baseDir)
		if err != nil {
			return nil, "", err
		}
		return provider, "docker", nil
	default:
		return nil, "", fmt.Errorf("unsupported workspace runtime %q", name)
	}
}
