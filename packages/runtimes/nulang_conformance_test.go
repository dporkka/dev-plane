package runtimes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type conformanceSeeder struct {
	mu    sync.Mutex
	calls int
}

func (s *conformanceSeeder) Seed(_ context.Context, _ *NulangCloudProvider, _ string, _ CreateRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return nil
}

type fakeWorkspaceAuthority struct {
	mu               sync.Mutex
	idempotencyKey   string
	workspaceID      string
	createIntent     map[string]any
	created          bool
	lostAckDelivered bool
	destroyed        bool
	patched          bool
	restored         bool
}

func (a *fakeWorkspaceAuthority) handler(loseFirstAck bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				http.Error(w, "missing idempotency key", http.StatusBadRequest)
				return
			}
			id, _ := body["id"].(string)
			if a.created {
				if key != a.idempotencyKey || id != a.workspaceID {
					http.Error(w, "idempotency conflict", http.StatusConflict)
					return
				}
			} else {
				a.created = true
				a.idempotencyKey = key
				a.workspaceID = id
				a.createIntent = body
			}
			if loseFirstAck && !a.lostAckDelivered {
				a.lostAckDelivered = true
				panic(http.ErrAbortHandler)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workspace": map[string]any{
					"workspace_id": a.workspaceID,
					"status":       "running",
					"memory_mb":    int(a.createIntent["memory_mb"].(float64)),
					"vcpus":        int(a.createIntent["vcpus"].(float64)),
					"vsock_cid":    3001,
				},
				"guest_ready": true,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-conformance/exec":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind":             "exec",
				"exit_code":        0,
				"timed_out":        false,
				"stdout_base64":    base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef01234567\n")),
				"stderr_base64":    "",
				"stdout_truncated": false,
				"stderr_truncated": false,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-conformance/files/write":
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "ack"})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-conformance/files/read":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind":           "file",
				"path":           "README.md",
				"size":           5,
				"content_base64": base64.StdEncoding.EncodeToString([]byte("hello")),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-conformance/checkpoints":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"checkpoint": map[string]any{"id": "cp-1", "workspace_id": "ws-conformance"},
				"restarted":  true,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-conformance/checkpoints/cp-1/restore":
			a.restored = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"checkpoint": map[string]any{"id": "cp-1", "workspace_id": "ws-conformance"},
				"restarted":  true,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces/ws-conformance/usage":
			_ = json.NewEncoder(w).Encode(RuntimeUsage{
				CPUMilliseconds:    1500,
				MemoryMBSeconds:    8192,
				StorageByteSeconds: 4096,
				EgressBytes:        0,
				SnapshotBytes:      2048,
				RuntimeCostUSD:     0,
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/workspaces/ws-conformance":
			a.destroyed = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "ws-conformance", "status": "destroyed"})
		default:
			http.NotFound(w, r)
		}
	})
}

func TestNulangProviderConformanceLostCreateAckThenReplay(t *testing.T) {
	authority := &fakeWorkspaceAuthority{}
	seeder := &conformanceSeeder{}
	req := CreateRequest{
		RepositoryID:   "repo-1",
		CloneURL:       "https://example.invalid/repo.git",
		Branch:         "agent/task-1/initial",
		BaseBranch:     "main",
		WorktreeName:   "ws-conformance",
		IdempotencyKey: "workspace:run-1",
		Limits: ResourceLimits{
			CPUMillis: 2000,
			MemoryMB:  4096,
			DiskMB:    10240,
		},
		Capabilities: RuntimeCapabilities{Network: false},
	}

	firstServer := httptest.NewServer(authority.handler(true))
	first := NewNulangCloudProvider(firstServer.URL, "internal-secret").
		WithHTTPClient(firstServer.Client()).
		WithRepositorySeeder(seeder)
	if _, err := first.CreateWorkspace(context.Background(), req); err == nil {
		t.Fatal("first CreateWorkspace() error = nil, want lost acknowledgement")
	}
	firstServer.Close()

	secondServer := httptest.NewServer(authority.handler(false))
	defer secondServer.Close()
	provider := NewNulangCloudProvider(secondServer.URL, "internal-secret").
		WithHTTPClient(secondServer.Client()).
		WithRepositorySeeder(seeder)

	session, err := provider.CreateWorkspace(context.Background(), req)
	if err != nil {
		t.Fatalf("replayed CreateWorkspace() error = %v", err)
	}
	if session.ID != "ws-conformance" {
		t.Fatalf("session.ID = %q", session.ID)
	}
	if authority.idempotencyKey != req.IdempotencyKey {
		t.Fatalf("authority idempotency key = %q", authority.idempotencyKey)
	}

	head, err := provider.ExecuteCommand(context.Background(), session.ID, Command{Args: []string{"git", "rev-parse", "HEAD"}})
	if err != nil {
		t.Fatalf("ExecuteCommand() error = %v", err)
	}
	if head.Stdout != "0123456789abcdef0123456789abcdef01234567\n" {
		t.Fatalf("HEAD = %q", head.Stdout)
	}

	if err := provider.WriteFile(context.Background(), session.ID, "README.md", []byte("hello")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	data, err := provider.ReadFile(context.Background(), session.ID, "README.md")
	if err != nil || string(data) != "hello" {
		t.Fatalf("ReadFile() = %q, %v", data, err)
	}

	snap, err := provider.Snapshot(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if err := provider.Restore(context.Background(), session.ID, snap); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	usage, err := provider.GetUsage(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.CPUMilliseconds != 1500 || usage.MemoryMBSeconds != 8192 {
		t.Fatalf("usage = %#v", usage)
	}
	if err := provider.DestroyWorkspace(context.Background(), session.ID); err != nil {
		t.Fatalf("DestroyWorkspace() error = %v", err)
	}
	if !authority.restored || !authority.destroyed {
		t.Fatalf("restore/destroy = %v/%v", authority.restored, authority.destroyed)
	}
}
