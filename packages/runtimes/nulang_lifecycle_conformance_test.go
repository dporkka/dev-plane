package runtimes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type retiringWorkspaceAuthority struct {
	mu             sync.Mutex
	idempotencyKey string
	workspaceID    string
	created        bool
	tombstoned     bool
}

func (a *retiringWorkspaceAuthority) handler() http.Handler {
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
			id, _ := body["id"].(string)
			if a.tombstoned && id == a.workspaceID {
				http.Error(w, "workspace identity retired", http.StatusConflict)
				return
			}
			if !a.created {
				a.created = true
				a.idempotencyKey = key
				a.workspaceID = id
			} else if key != a.idempotencyKey || id != a.workspaceID {
				http.Error(w, "idempotency conflict", http.StatusConflict)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workspace": map[string]any{
					"workspace_id": a.workspaceID,
					"status":       "running",
					"memory_mb":    body["memory_mb"],
					"vcpus":        body["vcpus"],
					"vsock_cid":    3001,
				},
				"guest_ready": true,
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/workspaces/ws-conformance":
			a.tombstoned = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "ws-conformance",
				"status": "destroyed",
			})
		default:
			http.NotFound(w, r)
		}
	})
}

func TestNulangProviderConformanceDestroyedWorkspaceCannotBeResurrected(t *testing.T) {
	authority := &retiringWorkspaceAuthority{}
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

	session, err := provider.CreateWorkspace(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	if err := provider.DestroyWorkspace(context.Background(), session.ID); err != nil {
		t.Fatalf("DestroyWorkspace() error = %v", err)
	}

	if _, err := provider.CreateWorkspace(context.Background(), req); err == nil {
		t.Fatal("original create key resurrected destroyed workspace")
	}

	newKey := req
	newKey.IdempotencyKey = "workspace:run-2"
	if _, err := provider.CreateWorkspace(context.Background(), newKey); err == nil {
		t.Fatal("new create key reclaimed retired workspace identity")
	}
}
