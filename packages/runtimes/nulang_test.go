package runtimes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNulangCloudProviderCreateWorkspaceCarriesRuntimeContract(t *testing.T) {
	var got CreateRequest
	var authHeader string
	var idempotencyKey string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workspaces" {
			http.NotFound(w, r)
			return
		}
		authHeader = r.Header.Get("X-Internal-Auth-Token")
		idempotencyKey = r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Session{ID: "nlc-session", WorkspaceID: "repo-1", Status: "ready", Provider: "nulang"})
	}))
	defer server.Close()

	provider := NewNulangCloudProvider(server.URL, "internal-secret").WithHTTPClient(server.Client())
	session, err := provider.CreateWorkspace(context.Background(), CreateRequest{
		RepositoryID: "repo-1",
		CloneURL:     "https://example.invalid/repo.git",
		Branch:       "feat/browser-verification",
		BaseBranch:   "main",
		Limits: ResourceLimits{
			CPUMillis:       2000,
			MemoryMB:        4096,
			DiskMB:          10240,
			WallTimeSeconds: 1800,
		},
		Capabilities: RuntimeCapabilities{
			Network: false,
			Secrets: []string{"github-token"},
		},
		Metadata: map[string]string{
			"dev_plane_task_id": "task-1",
			"dev_plane_run_id":  "run-1",
		},
		IdempotencyKey: "workspace:run-1",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	if session.Provider != "nulang" {
		t.Fatalf("session.Provider = %q, want nulang", session.Provider)
	}
	if authHeader != "internal-secret" {
		t.Fatalf("X-Internal-Auth-Token = %q", authHeader)
	}
	if idempotencyKey != "workspace:run-1" {
		t.Fatalf("Idempotency-Key = %q", idempotencyKey)
	}
	if got.Limits.MemoryMB != 4096 || got.Limits.CPUMillis != 2000 {
		t.Fatalf("Limits = %#v", got.Limits)
	}
	if got.Capabilities.Network {
		t.Fatal("Capabilities.Network = true, want false")
	}
	if len(got.Capabilities.Secrets) != 1 || got.Capabilities.Secrets[0] != "github-token" {
		t.Fatalf("Capabilities.Secrets = %#v", got.Capabilities.Secrets)
	}
	if got.Metadata["dev_plane_run_id"] != "run-1" {
		t.Fatalf("Metadata = %#v", got.Metadata)
	}
}

func TestNulangCloudProviderReturnsRuntimeUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workspaces/session-1/usage" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Internal-Auth-Token"); got != "internal-secret" {
			t.Fatalf("X-Internal-Auth-Token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RuntimeUsage{
			CPUMilliseconds:     1500,
			MemoryMBSeconds:     8192,
			StorageByteSeconds:  4096,
			EgressBytes:         1024,
			SnapshotBytes:       2048,
			RuntimeCostUSD:      0.021,
		})
	}))
	defer server.Close()

	provider := NewNulangCloudProvider(server.URL, "internal-secret").WithHTTPClient(server.Client())
	usage, err := provider.GetUsage(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.CPUMilliseconds != 1500 || usage.RuntimeCostUSD != 0.021 {
		t.Fatalf("usage = %#v", usage)
	}
}

var _ Provider = (*NulangCloudProvider)(nil)
var _ UsageProvider = (*NulangCloudProvider)(nil)
