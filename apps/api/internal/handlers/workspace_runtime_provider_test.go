package handlers

import (
	"log/slog"
	"os"
	"testing"

	"github.com/ai-dev-control-plane/runtimes"
)

func TestRuntimeProviderUsesNulangClientForNulangWorkspace(t *testing.T) {
	t.Setenv("RUNNER_URL", "http://localhost:8096")
	t.Setenv("RUNNER_AUTH_TOKEN", "internal-token")

	h := NewHandler(nil, slog.Default())
	provider, err := h.runtimeProvider("nulang")
	if err != nil {
		t.Fatalf("runtimeProvider(nulang) error: %v", err)
	}
	if _, ok := provider.(*runtimes.NulangCloudProvider); !ok {
		t.Fatalf("provider type = %T, want *runtimes.NulangCloudProvider", provider)
	}
}

func TestRuntimeProviderKeepsGenericRemoteForOtherRemoteWorkspaces(t *testing.T) {
	t.Setenv("RUNNER_URL", "http://localhost:8082")
	t.Setenv("RUNNER_AUTH_TOKEN", "runner-token")

	h := NewHandler(nil, slog.Default())
	provider, err := h.runtimeProvider("docker")
	if err != nil {
		t.Fatalf("runtimeProvider(docker) error: %v", err)
	}
	if _, ok := provider.(*runtimes.RemoteProvider); !ok {
		t.Fatalf("provider type = %T, want *runtimes.RemoteProvider", provider)
	}

	_ = os.Getenv("RUNNER_URL")
}
