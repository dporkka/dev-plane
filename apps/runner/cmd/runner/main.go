// Package main implements the runner (sandbox/runtime) service for the
// AI Dev Control Plane.
//
// The runner exposes the workspace runtime Provider over HTTP so that the
// worker and API can create workspaces, execute commands, and manipulate files
// in a remote sandbox.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ai-dev-control-plane/runner/internal/config"
	"github.com/ai-dev-control-plane/runner/internal/server"
	"github.com/ai-dev-control-plane/runtimes"
)

func main() {
	cfg := config.Load()
	logger := newLogger(cfg.LogLevel)

	provider, providerName, err := runtimes.NewProvider(cfg.Runtime, cfg.RuntimeBaseDir, os.Getenv("RUNNER_URL"), os.Getenv("RUNNER_AUTH_TOKEN"))
	if err != nil {
		logger.Error("failed to initialize workspace runtime", "error", err)
		os.Exit(1)
	}

	logger.Info("starting runner service",
		"workspace_runtime", providerName,
		"workspace_base_dir", cfg.RuntimeBaseDir,
		"addr", cfg.Address(),
	)

	srv := server.New(cfg, provider, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		logger.Error("runner service failed", "error", err)
		os.Exit(1)
	}

	logger.Info("runner service stopped gracefully")
}

func newLogger(levelStr string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if levelStr != "" {
		var level slog.Level
		if err := level.UnmarshalText([]byte(levelStr)); err == nil {
			opts.Level = level
		}
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
