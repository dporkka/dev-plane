package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/runtimes"
)

func TestRuntimeProviderNulangCloudUsesWorkspaceAPI(t *testing.T) {
	var cloudHit bool
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudHit = true
		if r.URL.Path != "/workspaces/ws-1" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Internal-Auth-Token"); got != "internal-token" {
			t.Fatalf("Nulang Cloud auth = %q, want internal-token", got)
		}
		_, _ = w.Write([]byte(`{"id":"ws-1","status":"running","guest_ready":true}`))
	}))
	defer cloud.Close()

	var legacyHit bool
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		legacyHit = true
		http.Error(w, "legacy runner must not receive Nulang Cloud traffic", http.StatusInternalServerError)
	}))
	defer legacy.Close()

	t.Setenv("RUNNER_URL", legacy.URL)
	t.Setenv("RUNNER_AUTH_TOKEN", "legacy-runner-token")
	t.Setenv("NULANG_CLOUD_URL", cloud.URL)
	t.Setenv("NULANG_CLOUD_TOKEN", "internal-token")

	h := &Handler{}
	provider, err := h.runtimeProvider("nulang-cloud")
	if err != nil {
		t.Fatalf("runtimeProvider(nulang-cloud) error: %v", err)
	}
	if _, ok := provider.(*runtimes.NulangCloudProvider); !ok {
		t.Fatalf("provider type = %T, want *runtimes.NulangCloudProvider", provider)
	}
	if _, err := provider.GetStatus(context.Background(), "ws-1"); err != nil {
		t.Fatalf("GetStatus through Nulang Cloud provider: %v", err)
	}
	if !cloudHit || legacyHit {
		t.Fatalf("routing cloudHit=%v legacyHit=%v", cloudHit, legacyHit)
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
