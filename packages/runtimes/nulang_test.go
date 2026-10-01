package runtimes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

type fakeNulangSeeder struct {
	workspaceID string
	req         CreateRequest
	err         error
}

func (s *fakeNulangSeeder) Seed(_ context.Context, _ *NulangCloudProvider, workspaceID string, req CreateRequest) error {
	s.workspaceID = workspaceID
	s.req = req
	return s.err
}

func TestNulangCloudProviderCreateWorkspaceUsesWorkspaceFoundationAPI(t *testing.T) {
	var got struct {
		ID       string `json:"id"`
		MemoryMB int    `json:"memory_mb"`
		VCPUs    int    `json:"vcpus"`
	}
	var authHeader string
	var idempotencyKey string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspaces" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		authHeader = r.Header.Get("X-Internal-Auth-Token")
		idempotencyKey = r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode create request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"workspace": map[string]any{
				"id":        got.ID,
				"status":    "running",
				"memory_mb": got.MemoryMB,
				"vcpus":     got.VCPUs,
				"vsock_cid": 3001,
			},
			"guest_ready": true,
		})
	}))
	defer server.Close()

	seeder := &fakeNulangSeeder{}
	provider := NewNulangCloudProvider(server.URL, "internal-secret").
		WithHTTPClient(server.Client()).
		WithRepositorySeeder(seeder)

	req := CreateRequest{
		RepositoryID: "repo-1",
		CloneURL:     "https://example.invalid/repo.git",
		Branch:       "agent/task-1/initial",
		BaseBranch:   "main",
		WorktreeName: "workspace-task-1",
		Limits: ResourceLimits{
			CPUMillis:       2000,
			MemoryMB:        4096,
			DiskMB:          10240,
			WallTimeSeconds: 1800,
		},
		Capabilities: RuntimeCapabilities{Network: false},
		Metadata: map[string]string{
			"dev_plane_task_id": "task-1",
			"dev_plane_run_id":  "run-1",
		},
		IdempotencyKey: "workspace:run-1",
	}

	session, err := provider.CreateWorkspace(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	if session.ID != "workspace-task-1" || session.Provider != "nulang" || session.Status != "ready" {
		t.Fatalf("session = %#v", session)
	}
	if got.ID != "workspace-task-1" || got.MemoryMB != 4096 || got.VCPUs != 2 {
		t.Fatalf("create request = %#v", got)
	}
	if authHeader != "internal-secret" {
		t.Fatalf("X-Internal-Auth-Token = %q", authHeader)
	}
	if idempotencyKey != "workspace:run-1" {
		t.Fatalf("Idempotency-Key = %q", idempotencyKey)
	}
	if seeder.workspaceID != "workspace-task-1" || !reflect.DeepEqual(seeder.req, req) {
		t.Fatalf("seeder call = workspace %q req %#v", seeder.workspaceID, seeder.req)
	}
}

func TestNulangCloudProviderCreateWorkspaceRejectsUnsupportedAuthority(t *testing.T) {
	provider := NewNulangCloudProvider("http://127.0.0.1:1", "token").WithRepositorySeeder(&fakeNulangSeeder{})

	_, err := provider.CreateWorkspace(context.Background(), CreateRequest{
		RepositoryID: "repo-1",
		WorktreeName: "workspace-task-1",
		Capabilities: RuntimeCapabilities{Network: true},
	})
	if err == nil || !errors.Is(err, ErrUnsupportedRuntimeCapability) {
		t.Fatalf("network CreateWorkspace() error = %v, want ErrUnsupportedRuntimeCapability", err)
	}

	_, err = provider.CreateWorkspace(context.Background(), CreateRequest{
		RepositoryID: "repo-1",
		WorktreeName: "workspace-task-1",
		Capabilities: RuntimeCapabilities{Secrets: []string{"github-token"}},
	})
	if err == nil || !errors.Is(err, ErrUnsupportedRuntimeCapability) {
		t.Fatalf("secret CreateWorkspace() error = %v, want ErrUnsupportedRuntimeCapability", err)
	}
}

func TestNulangCloudProviderExecReadWriteAndStatusMapWorkspaceAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Auth-Token"); got != "internal-secret" {
			t.Fatalf("X-Internal-Auth-Token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/exec":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode exec: %v", err)
			}
			if req["command"] != "git" || req["cwd"] != "repo" {
				t.Fatalf("exec request = %#v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind":             "exec",
				"exit_code":        0,
				"timed_out":        false,
				"stdout_base64":    base64.StdEncoding.EncodeToString([]byte("abc123\n")),
				"stderr_base64":    "",
				"stdout_truncated": false,
				"stderr_truncated": false,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/files/read":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind":           "file",
				"path":           "README.md",
				"size":           5,
				"content_base64": base64.StdEncoding.EncodeToString([]byte("hello")),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/files/write":
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "ack"})
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces/ws-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "ws-1",
				"status":      "running",
				"guest_ready": true,
				"memory_mb":   4096,
				"vcpus":       2,
				"vsock_cid":   3001,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewNulangCloudProvider(server.URL, "internal-secret").WithHTTPClient(server.Client())
	result, err := provider.ExecuteCommand(context.Background(), "ws-1", Command{
		Args:    []string{"git", "rev-parse", "HEAD"},
		Dir:     "repo",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand() error = %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "abc123\n" {
		t.Fatalf("command result = %#v", result)
	}

	data, err := provider.ReadFile(context.Background(), "ws-1", "README.md")
	if err != nil || string(data) != "hello" {
		t.Fatalf("ReadFile() = %q, %v", data, err)
	}
	if err := provider.WriteFile(context.Background(), "ws-1", "new.txt", []byte("content")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	status, err := provider.GetStatus(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if status.Status != "ready" || status.SessionID != "ws-1" {
		t.Fatalf("status = %#v", status)
	}
}

func TestNulangCloudProviderCheckpointRestoreAndDestroyMapWorkspaceAPI(t *testing.T) {
	var restorePath string
	var destroyed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/checkpoints":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"checkpoint": map[string]any{"id": "cp-1", "workspace_id": "ws-1"},
				"restarted":  true,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/checkpoints/cp-1/restore":
			restorePath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{
				"checkpoint": map[string]any{"id": "cp-1", "workspace_id": "ws-1"},
				"restarted":  true,
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/workspaces/ws-1":
			destroyed = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "ws-1", "status": "destroyed"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewNulangCloudProvider(server.URL, "internal-secret").WithHTTPClient(server.Client())
	snap, err := provider.Snapshot(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snap.ID != "cp-1" || snap.SessionID != "ws-1" {
		t.Fatalf("snapshot = %#v", snap)
	}
	if err := provider.Restore(context.Background(), "ws-1", snap); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restorePath == "" {
		t.Fatal("restore endpoint was not called")
	}
	if err := provider.DestroyWorkspace(context.Background(), "ws-1"); err != nil {
		t.Fatalf("DestroyWorkspace() error = %v", err)
	}
	if !destroyed {
		t.Fatal("destroy endpoint was not called")
	}
}

func TestNulangCloudProviderGetUsageMapsWorkspaceUsageEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/workspaces/ws-1/usage" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RuntimeUsage{
			CPUMilliseconds:    1500,
			MemoryMBSeconds:    8192,
			StorageByteSeconds: 4096,
			EgressBytes:        0,
			SnapshotBytes:      2048,
			RuntimeCostUSD:     0,
		})
	}))
	defer server.Close()

	provider := NewNulangCloudProvider(server.URL, "internal-secret").WithHTTPClient(server.Client())
	usage, err := provider.GetUsage(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.CPUMilliseconds != 1500 || usage.MemoryMBSeconds != 8192 || usage.SnapshotBytes != 2048 {
		t.Fatalf("usage = %#v", usage)
	}
}

var _ Provider = (*NulangCloudProvider)(nil)
var _ UsageProvider = (*NulangCloudProvider)(nil)
