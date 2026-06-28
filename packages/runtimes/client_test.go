package runtimes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// fakeProvider is a minimal Provider implementation for testing the remote client.
type fakeProvider struct {
	sessions map[string]*Session
	commands []Command
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{sessions: make(map[string]*Session)}
}

func (p *fakeProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	sess := &Session{
		ID:           "sess-" + req.RepositoryID,
		WorkspaceID:  req.RepositoryID,
		Status:       "ready",
		Provider:     "fake",
		WorktreePath: "/tmp/" + req.RepositoryID,
		CreatedAt:    time.Now(),
	}
	p.sessions[sess.ID] = sess
	return sess, nil
}

func (p *fakeProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	if _, ok := p.sessions[sessionID]; !ok {
		return ErrSessionNotFound
	}
	delete(p.sessions, sessionID)
	return nil
}

func (p *fakeProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd Command) (*CommandResult, error) {
	if _, ok := p.sessions[sessionID]; !ok {
		return nil, ErrSessionNotFound
	}
	p.commands = append(p.commands, cmd)
	return &CommandResult{Stdout: "ok", ExitCode: 0, Duration: time.Millisecond}, nil
}

func (p *fakeProvider) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	if _, ok := p.sessions[sessionID]; !ok {
		return nil, ErrSessionNotFound
	}
	return []byte("contents of " + path), nil
}

func (p *fakeProvider) WriteFile(ctx context.Context, sessionID, path string, data []byte) error {
	if _, ok := p.sessions[sessionID]; !ok {
		return ErrSessionNotFound
	}
	return nil
}

func (p *fakeProvider) ApplyPatch(ctx context.Context, sessionID, patch string) error {
	if _, ok := p.sessions[sessionID]; !ok {
		return ErrSessionNotFound
	}
	return nil
}

func (p *fakeProvider) Snapshot(ctx context.Context, sessionID string) (*Snapshot, error) {
	if _, ok := p.sessions[sessionID]; !ok {
		return nil, ErrSessionNotFound
	}
	return &Snapshot{ID: "snap-1", SessionID: sessionID, CreatedAt: time.Now()}, nil
}

func (p *fakeProvider) Restore(ctx context.Context, sessionID string, snap *Snapshot) error {
	if _, ok := p.sessions[sessionID]; !ok {
		return ErrSessionNotFound
	}
	return nil
}

func (p *fakeProvider) GetStatus(ctx context.Context, sessionID string) (*SessionStatus, error) {
	if _, ok := p.sessions[sessionID]; !ok {
		return nil, ErrSessionNotFound
	}
	return &SessionStatus{SessionID: sessionID, Status: "ready", LastActive: time.Now()}, nil
}

func (p *fakeProvider) StreamLogs(ctx context.Context, sessionID string) (<-chan LogLine, error) {
	if _, ok := p.sessions[sessionID]; !ok {
		return nil, ErrSessionNotFound
	}
	out := make(chan LogLine, 2)
	out <- LogLine{Timestamp: time.Now(), Stream: "stdout", Message: "log1"}
	out <- LogLine{Timestamp: time.Now(), Stream: "stdout", Message: "log2"}
	close(out)
	return out, nil
}

func startTestRunnerServer(t *testing.T, provider Provider, authToken string) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	h := &testHandler{provider: provider, logger: slog.New(slog.NewTextHandler(os.Stdout, nil)), authToken: authToken}
	h.registerRoutes(r)
	return httptest.NewServer(r)
}

type testHandler struct {
	provider  Provider
	logger    *slog.Logger
	authToken string
}

func (h *testHandler) registerRoutes(r chi.Router) {
	if h.authToken != "" {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				auth := r.Header.Get("Authorization")
				if auth != "Bearer "+h.authToken {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
			})
		})
	}
	r.Post("/v1/workspaces", h.createWorkspace)
	r.Delete("/v1/workspaces/{sessionID}", h.destroyWorkspace)
	r.Post("/v1/workspaces/{sessionID}/commands", h.executeCommand)
	r.Get("/v1/workspaces/{sessionID}/files/*", h.readFile)
	r.Put("/v1/workspaces/{sessionID}/files/*", h.writeFile)
	r.Post("/v1/workspaces/{sessionID}/patches", h.applyPatch)
	r.Get("/v1/workspaces/{sessionID}/snapshot", h.snapshot)
	r.Post("/v1/workspaces/{sessionID}/restore", h.restore)
	r.Get("/v1/workspaces/{sessionID}/status", h.status)
	r.Get("/v1/workspaces/{sessionID}/logs", h.streamLogs)
}

func (h *testHandler) json(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *testHandler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sess, err := h.provider.CreateWorkspace(r.Context(), req)
	if err != nil {
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.json(w, http.StatusCreated, sess)
}

func (h *testHandler) destroyWorkspace(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionID")
	if err := h.provider.DestroyWorkspace(r.Context(), sid); err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			h.json(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.json(w, http.StatusOK, map[string]string{"status": "destroyed"})
}

func (h *testHandler) executeCommand(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionID")
	var cmd Command
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := h.provider.ExecuteCommand(r.Context(), sid, cmd)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			h.json(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.json(w, http.StatusOK, res)
}

func (h *testHandler) readFile(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionID")
	path := chi.URLParam(r, "*")
	data, err := h.provider.ReadFile(r.Context(), sid, path)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			h.json(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Write(data)
}

func (h *testHandler) writeFile(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionID")
	path := chi.URLParam(r, "*")
	data, _ := io.ReadAll(r.Body)
	if err := h.provider.WriteFile(r.Context(), sid, path, data); err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			h.json(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.json(w, http.StatusOK, map[string]string{"status": "written"})
}

func (h *testHandler) applyPatch(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionID")
	patch, _ := io.ReadAll(r.Body)
	if err := h.provider.ApplyPatch(r.Context(), sid, string(patch)); err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			h.json(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.json(w, http.StatusOK, map[string]string{"status": "patched"})
}

func (h *testHandler) snapshot(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionID")
	snap, err := h.provider.Snapshot(r.Context(), sid)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			h.json(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.json(w, http.StatusOK, snap)
}

func (h *testHandler) restore(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionID")
	var snap Snapshot
	if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
		h.json(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.provider.Restore(r.Context(), sid, &snap); err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			h.json(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.json(w, http.StatusOK, map[string]string{"status": "restored"})
}

func (h *testHandler) status(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionID")
	status, err := h.provider.GetStatus(r.Context(), sid)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			h.json(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.json(w, http.StatusOK, status)
}

func (h *testHandler) streamLogs(w http.ResponseWriter, r *http.Request) {
	sid := chi.URLParam(r, "sessionID")
	lines, err := h.provider.StreamLogs(r.Context(), sid)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			h.json(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.json(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	for line := range lines {
		data, _ := json.Marshal(line)
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func TestRemoteProviderCreateAndDestroyWorkspace(t *testing.T) {
	provider := newFakeProvider()
	server := startTestRunnerServer(t, provider, "")
	defer server.Close()

	client := NewRemoteProvider(server.URL, "")
	ctx := context.Background()

	sess, err := client.CreateWorkspace(ctx, CreateRequest{RepositoryID: "repo-1", CloneURL: "https://example.invalid/repo.git", Branch: "feat", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("CreateWorkspace error: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("expected session ID")
	}

	if err := client.DestroyWorkspace(ctx, sess.ID); err != nil {
		t.Fatalf("DestroyWorkspace error: %v", err)
	}

	if err := client.DestroyWorkspace(ctx, sess.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestRemoteProviderExecuteCommand(t *testing.T) {
	provider := newFakeProvider()
	server := startTestRunnerServer(t, provider, "")
	defer server.Close()

	client := NewRemoteProvider(server.URL, "")
	ctx := context.Background()

	sess, _ := client.CreateWorkspace(ctx, CreateRequest{RepositoryID: "repo-1", CloneURL: "https://example.invalid/repo.git", Branch: "feat", BaseBranch: "main"})
	res, err := client.ExecuteCommand(ctx, sess.ID, Command{Command: "echo hi"})
	if err != nil {
		t.Fatalf("ExecuteCommand error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
}

func TestRemoteProviderReadWriteFile(t *testing.T) {
	provider := newFakeProvider()
	server := startTestRunnerServer(t, provider, "")
	defer server.Close()

	client := NewRemoteProvider(server.URL, "")
	ctx := context.Background()

	sess, _ := client.CreateWorkspace(ctx, CreateRequest{RepositoryID: "repo-1", CloneURL: "https://example.invalid/repo.git", Branch: "feat", BaseBranch: "main"})
	if err := client.WriteFile(ctx, sess.ID, "test.txt", []byte("hello")); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	data, err := client.ReadFile(ctx, sess.ID, "test.txt")
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(data) == "" {
		t.Fatal("expected file contents")
	}
}

func TestRemoteProviderStreamLogs(t *testing.T) {
	provider := newFakeProvider()
	server := startTestRunnerServer(t, provider, "")
	defer server.Close()

	client := NewRemoteProvider(server.URL, "")
	ctx := context.Background()

	sess, _ := client.CreateWorkspace(ctx, CreateRequest{RepositoryID: "repo-1", CloneURL: "https://example.invalid/repo.git", Branch: "feat", BaseBranch: "main"})
	lines, err := client.StreamLogs(ctx, sess.ID)
	if err != nil {
		t.Fatalf("StreamLogs error: %v", err)
	}

	count := 0
	for range lines {
		count++
	}
	if count != 2 {
		t.Fatalf("got %d log lines, want 2", count)
	}
}

func TestRemoteProviderAuthToken(t *testing.T) {
	provider := newFakeProvider()
	server := startTestRunnerServer(t, provider, "secret")
	defer server.Close()

	client := NewRemoteProvider(server.URL, "secret")
	ctx := context.Background()
	_, err := client.CreateWorkspace(ctx, CreateRequest{RepositoryID: "repo-1", CloneURL: "https://example.invalid/repo.git", Branch: "feat", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("CreateWorkspace with valid token error: %v", err)
	}

	badClient := NewRemoteProvider(server.URL, "wrong")
	_, err = badClient.CreateWorkspace(ctx, CreateRequest{RepositoryID: "repo-2", CloneURL: "https://example.invalid/repo.git", Branch: "feat", BaseBranch: "main"})
	if err == nil {
		t.Fatal("expected auth error")
	}
}
