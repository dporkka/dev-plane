package config

import (
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("RUNNER_PORT", "")
	t.Setenv("PORT", "")

	cfg := Load()
	if cfg.Port != 8082 {
		t.Fatalf("default Port = %d, want 8082", cfg.Port)
	}
	if cfg.Runtime != "local" {
		t.Fatalf("default Runtime = %q, want local", cfg.Runtime)
	}
}

func TestLoadNulangCloudRuntimeConnection(t *testing.T) {
	t.Setenv("WORKSPACE_RUNTIME", "nulang-cloud")
	t.Setenv("RUNNER_URL", "https://legacy-runner.test")
	t.Setenv("RUNNER_AUTH_TOKEN", "runner-token")
	t.Setenv("NULANG_CLOUD_URL", "https://cloud.nulang.test")
	t.Setenv("NULANG_CLOUD_TOKEN", "cloud-token")

	cfg := Load()
	if cfg.RuntimeURL != "https://cloud.nulang.test" {
		t.Fatalf("RuntimeURL = %q, want dedicated Nulang Cloud URL", cfg.RuntimeURL)
	}
	if cfg.RuntimeToken != "cloud-token" {
		t.Fatalf("RuntimeToken = %q, want dedicated Nulang Cloud token", cfg.RuntimeToken)
	}
	if cfg.AuthToken != "runner-token" {
		t.Fatalf("AuthToken = %q, want inbound runner token to remain separate", cfg.AuthToken)
	}
}

func TestLoadRunnerPort(t *testing.T) {
	t.Setenv("RUNNER_PORT", "9090")
	t.Setenv("PORT", "8080")

	cfg := Load()
	if cfg.Port != 9090 {
		t.Fatalf("Port = %d, want 9090", cfg.Port)
	}
}

func TestLoadRunnerPortTakesPrecedence(t *testing.T) {
	t.Setenv("RUNNER_PORT", "9090")
	t.Setenv("PORT", "7070")

	cfg := Load()
	if cfg.Port != 9090 {
		t.Fatalf("Port = %d, want 9090 (RUNNER_PORT should win)", cfg.Port)
	}
}

func TestLoadFallbackPort(t *testing.T) {
	t.Setenv("RUNNER_PORT", "")
	t.Setenv("PORT", "7070")

	cfg := Load()
	if cfg.Port != 7070 {
		t.Fatalf("Port = %d, want 7070", cfg.Port)
	}
}

func TestLoadInvalidRunnerPort(t *testing.T) {
	t.Setenv("RUNNER_PORT", "not-a-number")
	t.Setenv("PORT", "")

	cfg := Load()
	if cfg.Port != 8082 {
		t.Fatalf("Port = %d, want default 8082", cfg.Port)
	}
}

func TestAddress(t *testing.T) {
	cfg := &Config{Port: 8082}
	if got := cfg.Address(); got != ":8082" {
		t.Fatalf("Address() = %q, want :8082", got)
	}
}
