package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/runner/internal/config"
	"github.com/ai-dev-control-plane/runtimes"
)

// stubProvider is a minimal Provider implementation for server tests.
type stubProvider struct{}

func (stubProvider) CreateWorkspace(ctx context.Context, req runtimes.CreateRequest) (*runtimes.Session, error) {
	return &runtimes.Session{ID: "sess-1", WorkspaceID: req.RepositoryID, Status: "ready", Provider: "stub"}, nil
}

func (stubProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	if sessionID == "missing" {
		return runtimes.ErrSessionNotFound
	}
	return nil
}

func (stubProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd runtimes.Command) (*runtimes.CommandResult, error) {
	return &runtimes.CommandResult{Stdout: "ok", ExitCode: 0, Duration: time.Millisecond}, nil
}

func (stubProvider) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error)   { return []byte("hi"), nil }
func (stubProvider) WriteFile(ctx context.Context, sessionID, path string, data []byte) error { return nil }
func (stubProvider) ApplyPatch(ctx context.Context, sessionID, patch string) error            { return nil }
func (stubProvider) Snapshot(ctx context.Context, sessionID string) (*runtimes.Snapshot, error) {
	return &runtimes.Snapshot{ID: "snap-1", SessionID: sessionID}, nil
}
func (stubProvider) Restore(ctx context.Context, sessionID string, snap *runtimes.Snapshot) error { return nil }
func (stubProvider) GetStatus(ctx context.Context, sessionID string) (*runtimes.SessionStatus, error) {
	if sessionID == "__runner_ready_check__" {
		return nil, runtimes.ErrSessionNotFound
	}
	return &runtimes.SessionStatus{SessionID: sessionID, Status: "ready"}, nil
}
func (stubProvider) StreamLogs(ctx context.Context, sessionID string) (<-chan runtimes.LogLine, error) {
	out := make(chan runtimes.LogLine, 1)
	out <- runtimes.LogLine{Stream: "stdout", Message: "log"}
	close(out)
	return out, nil
}

func TestHealthAndReady(t *testing.T) {
	cfg := &config.Config{Port: 0, AuthToken: ""}
	srv := New(cfg, stubProvider{}, testLogger(t))

	r := chi.NewRouter()
	r.Get("/health", srv.healthCheck)
	r.Get("/ready", srv.readyCheck)

	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("health request error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/ready")
	if err != nil {
		t.Fatalf("ready request error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ready status = %d, want 200", resp.StatusCode)
	}
}

func TestAuthMiddlewareRejectsMissingToken(t *testing.T) {
	cfg := &config.Config{Port: 0, AuthToken: "secret"}
	srv := New(cfg, stubProvider{}, testLogger(t))

	r := chi.NewRouter()
	r.Use(srv.authMiddleware)
	r.Get("/health", srv.healthCheck)

	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCreateWorkspaceHandler(t *testing.T) {
	h := NewHandler(stubProvider{}, testLogger(t))

	r := chi.NewRouter()
	h.RegisterRoutes(r)

	ts := httptest.NewServer(r)
	defer ts.Close()

	reqBody, _ := json.Marshal(runtimes.CreateRequest{
		RepositoryID: "repo-1",
		CloneURL:     "https://example.invalid/repo.git",
		Branch:       "feat",
		BaseBranch:   "main",
	})
	resp, err := http.Post(ts.URL+"/v1/workspaces", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var sess runtimes.Session
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if sess.ID != "sess-1" {
		t.Fatalf("ID = %q, want sess-1", sess.ID)
	}
}

func TestDestroyMissingWorkspaceReturns404(t *testing.T) {
	h := NewHandler(stubProvider{}, testLogger(t))

	r := chi.NewRouter()
	h.RegisterRoutes(r)

	ts := httptest.NewServer(r)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/workspaces/missing", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
