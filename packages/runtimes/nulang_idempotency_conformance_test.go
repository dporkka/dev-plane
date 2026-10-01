package runtimes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

type resourceFencingWorkspaceAuthority struct {
	mu             sync.Mutex
	idempotencyKey string
	intent         map[string]any
}

func (a *resourceFencingWorkspaceAuthority) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/workspaces" {
			http.NotFound(w, r)
			return
		}
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
		if a.intent == nil {
			a.idempotencyKey = key
			a.intent = body
		} else if a.idempotencyKey != key || !reflect.DeepEqual(a.intent, body) {
			http.Error(w, "idempotency conflict", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"workspace": map[string]any{
				"workspace_id": body["id"],
				"status":       "running",
				"memory_mb":    body["memory_mb"],
				"vcpus":        body["vcpus"],
				"vsock_cid":    3001,
			},
			"guest_ready": true,
		})
	})
}

func TestNulangProviderConformanceRejectsDifferentKeyForExistingWorkspace(t *testing.T) {
	authority := &fakeWorkspaceAuthority{}
	server := httptest.NewServer(authority.handler(false))
	defer server.Close()
	seeder := &conformanceSeeder{}
	provider := NewNulangCloudProvider(server.URL, "internal-secret").
		WithHTTPClient(server.Client()).
		WithRepositorySeeder(seeder)

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
			DiskMB:    nulangWorkspaceDiskMB,
		},
	}
	if _, err := provider.CreateWorkspace(context.Background(), req); err != nil {
		t.Fatalf("first CreateWorkspace() error = %v", err)
	}

	conflict := req
	conflict.IdempotencyKey = "workspace:run-2"
	if _, err := provider.CreateWorkspace(context.Background(), conflict); err == nil {
		t.Fatal("different key for existing workspace succeeded, want conflict")
	}
	if len(authority.createIntent) == 0 || authority.idempotencyKey != req.IdempotencyKey {
		t.Fatalf("original authority was mutated: key=%q intent=%v", authority.idempotencyKey, authority.createIntent)
	}
}

func TestNulangProviderConformanceRejectsChangedResourcesForSameKey(t *testing.T) {
	authority := &resourceFencingWorkspaceAuthority{}
	server := httptest.NewServer(authority.handler())
	defer server.Close()
	provider := NewNulangCloudProvider(server.URL, "internal-secret").
		WithHTTPClient(server.Client()).
		WithRepositorySeeder(&conformanceSeeder{})

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
			DiskMB:    nulangWorkspaceDiskMB,
		},
	}
	if _, err := provider.CreateWorkspace(context.Background(), req); err != nil {
		t.Fatalf("first CreateWorkspace() error = %v", err)
	}

	changed := req
	changed.Limits.MemoryMB = 8192
	if _, err := provider.CreateWorkspace(context.Background(), changed); err == nil {
		t.Fatal("same key with changed resources succeeded, want conflict")
	}
}
