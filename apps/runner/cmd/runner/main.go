// Package main implements the runner (sandbox/runtime) service for the
// AI Dev Control Plane.
//
// The runner owns workspace runtime provisioning: it initializes the
// configured runtime Provider (a local worktree sandbox or a Docker
// container sandbox) where agents create workspaces, execute commands,
// read/write files, and apply patches. It runs until it receives
// SIGINT/SIGTERM and shuts the provider down gracefully.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ai-dev-control-plane/runtimes"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	var (
		logLevel       = flag.String("log-level", os.Getenv("LOG_LEVEL"), "Log level (debug, info, warn, error)")
		runtimeName    = flag.String("workspace-runtime", os.Getenv("WORKSPACE_RUNTIME"), "Workspace runtime: local or docker")
		runtimeBaseDir = flag.String("workspace-base-dir", os.Getenv("WORKSPACE_BASE_DIR"), "Workspace runtime base directory")
	)
	flag.Parse()

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

	provider, providerName, err := newRuntimeProvider(*runtimeName, *runtimeBaseDir)
	if err != nil {
		logger.Error("failed to initialize workspace runtime", "error", err)
		os.Exit(1)
	}
	// Some providers (e.g. Docker) hold resources that must be released on exit.
	defer func() {
		if closer, ok := provider.(interface{ Close() error }); ok {
			if cerr := closer.Close(); cerr != nil {
				logger.Warn("failed to close runtime provider", "error", cerr)
			}
		}
	}()

	logger.Info("starting runner service",
		"workspace_runtime", providerName,
		"workspace_base_dir", *runtimeBaseDir,
	)
	logger.Info("runner service is running, waiting for shutdown signal...")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan

	logger.Info("shutdown signal received", "signal", sig.String())
	logger.Info("runner service stopped gracefully")
}

// newRuntimeProvider constructs the workspace runtime provider selected by name.
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
