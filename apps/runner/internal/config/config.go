// Package config provides runtime configuration for the runner service.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds runner service configuration.
type Config struct {
	// Port is the HTTP port the runner listens on.
	Port int
	// Runtime is the workspace runtime provider name: local or docker.
	Runtime string
	// RuntimeBaseDir is the base directory for workspace storage.
	RuntimeBaseDir string
	// AuthToken is a shared secret required on incoming requests.
	AuthToken string
	// LogLevel is the slog level.
	LogLevel string
}

// Load returns a Config populated from environment variables and defaults.
func Load() *Config {
	cfg := &Config{
		Port:           8082,
		Runtime:        os.Getenv("WORKSPACE_RUNTIME"),
		RuntimeBaseDir: os.Getenv("WORKSPACE_BASE_DIR"),
		AuthToken:      os.Getenv("RUNNER_AUTH_TOKEN"),
		LogLevel:       os.Getenv("LOG_LEVEL"),
	}

	if cfg.Runtime == "" {
		cfg.Runtime = "local"
	}
	if cfg.RuntimeBaseDir == "" {
		cfg.RuntimeBaseDir = os.TempDir() + "/ai-dev-control-plane-workspaces"
	}
	if portStr := os.Getenv("PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			cfg.Port = port
		}
	}

	return cfg
}

// Address returns the listen address for the HTTP server.
func (c *Config) Address() string {
	return ":" + strconv.Itoa(c.Port)
}

// ShutdownTimeout is the maximum time to wait for graceful shutdown.
func ShutdownTimeout() time.Duration {
	return 30 * time.Second
}
