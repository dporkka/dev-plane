package handlers

import (
	"testing"

	"github.com/ai-dev-control-plane/runtimes"
)

func TestRuntimeProviderNulangCloudUsesWorkspaceAPI(t *testing.T) {
	t.Setenv("RUNNER_URL", "https://cloud.nulang.test")
	t.Setenv("RUNNER_AUTH_TOKEN", "internal-token")

	h := &Handler{}
	provider, err := h.runtimeProvider("nulang-cloud")
	if err != nil {
		t.Fatalf("runtimeProvider(nulang-cloud) error: %v", err)
	}
	if _, ok := provider.(*runtimes.NulangCloudProvider); !ok {
		t.Fatalf("provider type = %T, want *runtimes.NulangCloudProvider", provider)
	}
}

func TestRuntimeProviderPreservesLegacyRunnerURLOverride(t *testing.T) {
	t.Setenv("RUNNER_URL", "https://runner.test")
	t.Setenv("RUNNER_AUTH_TOKEN", "runner-token")

	h := &Handler{}
	provider, err := h.runtimeProvider("docker")
	if err != nil {
		t.Fatalf("runtimeProvider(docker with RUNNER_URL) error: %v", err)
	}
	if _, ok := provider.(*runtimes.RemoteProvider); !ok {
		t.Fatalf("provider type = %T, want *runtimes.RemoteProvider", provider)
	}
}
