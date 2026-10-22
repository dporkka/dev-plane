package runtimes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNulangCloudProviderCreateWorkspaceSendsSandboxSpec(t *testing.T) {
	var got struct {
		Spec      SandboxSpec `json:"spec"`
		Workspace struct {
			RepositoryID string `json:"repository_id"`
			CloneURL     string `json:"clone_url"`
			Branch       string `json:"branch"`
			BaseBranch   string `json:"base_branch"`
			WorktreeName string `json:"worktree_name"`
		} `json:"workspace"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/sandboxes" {
			t.Fatalf("request = %s %s, want POST /v1/sandboxes", r.Method, r.URL.Path)
		}
		if token := r.Header.Get("X-Internal-Auth-Token"); token != "secret" {
			t.Fatalf("internal auth token = %q, want secret", token)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("{\"id\":\"sandbox-1\",\"status\":\"ready\",\"created_at\":\"2026-09-30T20:00:00Z\"}"))
	}))
	defer server.Close()

	provider, err := NewNulangCloudProvider(server.URL, "secret")
	if err != nil {
		t.Fatalf("NewNulangCloudProvider() error: %v", err)
	}

	session, err := provider.CreateWorkspace(context.Background(), CreateRequest{
		RepositoryID: "repo-1",
		CloneURL:     "https://github.com/example/repo.git",
		Branch:       "feat/test",
		BaseBranch:   "main",
		WorktreeName: "task-1",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace() error: %v", err)
	}

	if session.ID != "sandbox-1" || session.Provider != "nulang-cloud" || session.WorkspaceID != "repo-1" {
		t.Fatalf("session = %+v", session)
	}
	if got.Spec.SchemaVersion != SandboxSchemaVersion {
		t.Fatalf("schema version = %q, want %q", got.Spec.SchemaVersion, SandboxSchemaVersion)
	}
	if got.Spec.Runtime != SandboxRuntimeAuto {
		t.Fatalf("runtime = %q, want auto", got.Spec.Runtime)
	}
	if !got.Spec.Workload.Interactive || !got.Spec.Workload.ArbitraryBinaries {
		t.Fatalf("coding-agent workload flags not set: %+v", got.Spec.Workload)
	}
	if got.Spec.Network.Mode != SandboxNetworkNone {
		t.Fatalf("network mode = %q, want none", got.Spec.Network.Mode)
	}
	if got.Workspace.RepositoryID != "repo-1" || got.Workspace.Branch != "feat/test" {
		t.Fatalf("workspace bootstrap = %+v", got.Workspace)
	}
}

func TestNulangCloudProviderRejectsInlineEnvironmentSecrets(t *testing.T) {
	provider, err := NewNulangCloudProvider("https://nlc.invalid", "secret")
	if err != nil {
		t.Fatalf("NewNulangCloudProvider() error: %v", err)
	}

	_, err = provider.CreateWorkspace(context.Background(), CreateRequest{
		RepositoryID: "repo-1",
		Env:          map[string]string{"GITHUB_TOKEN": "plaintext-secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "inline environment") {
		t.Fatalf("CreateWorkspace() error = %v, want inline environment rejection", err)
	}
}

func TestNulangCloudProviderLifecycleMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/sandbox-1/commands":
			_, _ = w.Write([]byte("{\"stdout\":\"ok\",\"stderr\":\"\",\"exit_code\":0,\"duration_ms\":1000000}"))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sandbox-1/files/README.md":
			_, _ = w.Write([]byte("hello"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/sandbox-1/snapshots":
			_, _ = w.Write([]byte("{\"id\":\"snap-1\",\"session_id\":\"sandbox-1\",\"description\":\"\",\"created_at\":\"2026-09-30T20:00:00Z\"}"))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sandbox-1/status":
			_, _ = w.Write([]byte("{\"session_id\":\"sandbox-1\",\"status\":\"ready\",\"last_active\":\"2026-09-30T20:00:00Z\"}"))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/sandbox-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider, err := NewNulangCloudProvider(server.URL, "")
	if err != nil {
		t.Fatalf("NewNulangCloudProvider() error: %v", err)
	}

	result, err := provider.ExecuteCommand(context.Background(), "sandbox-1", Command{Args: []string{"true"}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("ExecuteCommand() = %+v, %v", result, err)
	}
	data, err := provider.ReadFile(context.Background(), "sandbox-1", "README.md")
	if err != nil || string(data) != "hello" {
		t.Fatalf("ReadFile() = %q, %v", data, err)
	}
	snap, err := provider.Snapshot(context.Background(), "sandbox-1")
	if err != nil || snap.ID != "snap-1" {
		t.Fatalf("Snapshot() = %+v, %v", snap, err)
	}
	status, err := provider.GetStatus(context.Background(), "sandbox-1")
	if err != nil || status.Status != "ready" {
		t.Fatalf("GetStatus() = %+v, %v", status, err)
	}
	if err := provider.DestroyWorkspace(context.Background(), "sandbox-1"); err != nil {
		t.Fatalf("DestroyWorkspace() error: %v", err)
	}
}

func TestNewProviderNulangCloudFromEnv(t *testing.T) {
	t.Setenv("NULANG_CLOUD_URL", "https://nlc.example")
	t.Setenv("NULANG_CLOUD_TOKEN", "secret")

	p, name, err := NewProvider("nulang-cloud", "", "", "")
	if err != nil {
		t.Fatalf("NewProvider(nulang-cloud) error: %v", err)
	}
	if name != "nulang-cloud" {
		t.Fatalf("name = %q, want nulang-cloud", name)
	}
	if _, ok := p.(*NulangCloudProvider); !ok {
		t.Fatalf("provider type = %T, want *NulangCloudProvider", p)
	}
}

func TestDefaultDevPlaneSandboxSpecIsBounded(t *testing.T) {
	spec := DefaultDevPlaneSandboxSpec()
	if spec.Resources.CPUMillis <= 0 || spec.Resources.MemoryMB <= 0 || spec.Resources.MaxDurationSeconds <= 0 {
		t.Fatalf("unbounded default resources: %+v", spec.Resources)
	}
	if spec.Checkpoint.Mode != SandboxCheckpointManual {
		t.Fatalf("checkpoint mode = %q, want manual", spec.Checkpoint.Mode)
	}
	if spec.Resources.MaxDurationSeconds > int((2 * time.Hour).Seconds()) {
		t.Fatalf("default max duration too high: %d", spec.Resources.MaxDurationSeconds)
	}
}
