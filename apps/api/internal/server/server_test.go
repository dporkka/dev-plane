package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/config"
)

func TestNew(t *testing.T) {
	cfg := &config.Config{
		NATSURL:        "", // no NATS to avoid real connection
		JWTSecret:      "test-secret-that-is-at-least-thirty-two-bytes",
		Port:           "8080",
		LogLevel:       "info",
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	logger := slog.Default()

	s := New(cfg, nil, logger)
	if s == nil {
		t.Fatal("expected non-nil Server")
	}
	if s.router == nil {
		t.Fatal("expected non-nil router")
	}
}

func TestHandler(t *testing.T) {
	cfg := &config.Config{
		NATSURL:        "",
		JWTSecret:      "test-secret-that-is-at-least-thirty-two-bytes",
		Port:           "8080",
		LogLevel:       "info",
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	s := New(cfg, nil, slog.Default())
	h := s.Handler()
	if h == nil {
		t.Fatal("expected non-nil HTTP handler")
	}
}

func TestClose_NilDB(t *testing.T) {
	cfg := &config.Config{
		NATSURL:        "",
		JWTSecret:      "test-secret-that-is-at-least-thirty-two-bytes",
		Port:           "8080",
		LogLevel:       "info",
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	s := New(cfg, nil, slog.Default())
	if err := s.Close(); err != nil {
		t.Fatalf("Close() with nil DB should not error: %v", err)
	}
}

func TestHealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		NATSURL:        "",
		JWTSecret:      "test-secret-that-is-at-least-thirty-two-bytes",
		Port:           "8080",
		LogLevel:       "info",
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	s := New(cfg, nil, slog.Default())

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("health endpoint request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestReadyEndpoint(t *testing.T) {
	cfg := &config.Config{
		NATSURL:        "",
		JWTSecret:      "test-secret-that-is-at-least-thirty-two-bytes",
		Port:           "8080",
		LogLevel:       "info",
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	s := New(cfg, nil, slog.Default())

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ready")
	if err != nil {
		t.Fatalf("ready endpoint request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestForgeExecuteRouteRequiresWorkloadAndGiteaConfig(t *testing.T) {
	base := config.Config{
		NATSURL: "", JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes",
		AllowedOrigins:       []string{"http://localhost:3000"},
		NulangWorkloadID:     "nulang-cloud",
		NulangWorkloadSecret: "0123456789abcdef0123456789abcdef",
	}

	t.Run("mounted when both are configured", func(t *testing.T) {
		cfg := base
		cfg.GiteaBaseURL = "https://git.example.test"
		cfg.GiteaToken = "server-held-token"
		s := New(&cfg, nil, slog.Default())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/execute", strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 to prove execute route is mounted", rec.Code)
		}

		reconcileReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/reconcile", strings.NewReader("{}"))
		reconcileRec := httptest.NewRecorder()
		s.Handler().ServeHTTP(reconcileRec, reconcileReq)
		if reconcileRec.Code != http.StatusUnauthorized {
			t.Fatalf("reconcile status = %d, want 401 to prove route is mounted", reconcileRec.Code)
		}
	})

	t.Run("not mounted without gitea executor", func(t *testing.T) {
		cfg := base
		s := New(&cfg, nil, slog.Default())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/execute", strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		reconcileReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/reconcile", strings.NewReader("{}"))
		reconcileRec := httptest.NewRecorder()
		s.Handler().ServeHTTP(reconcileRec, reconcileReq)
		if reconcileRec.Code != http.StatusNotFound {
			t.Fatalf("reconcile status = %d, want 404", reconcileRec.Code)
		}
	})
}

func TestForgeExecutorProviderSelection(t *testing.T) {
	t.Run("gitea", func(t *testing.T) {
		cfg := &config.Config{
			ForgeProvider: "gitea",
			GiteaBaseURL: "https://git.example.test",
			GiteaToken: "gitea-token",
		}
		exec := newForgeExecutor(cfg, &http.Client{})
		if exec == nil || exec.Provider() != "gitea" {
			t.Fatalf("executor = %#v", exec)
		}
	})

	t.Run("github", func(t *testing.T) {
		cfg := &config.Config{
			ForgeProvider: "github",
			GitHubForgeBaseURL: "https://api.github.test",
			GitHubForgeToken: "github-token",
		}
		exec := newForgeExecutor(cfg, &http.Client{})
		if exec == nil || exec.Provider() != "github" {
			t.Fatalf("executor = %#v", exec)
		}
	})

	t.Run("none", func(t *testing.T) {
		if exec := newForgeExecutor(&config.Config{}, &http.Client{}); exec != nil {
			t.Fatalf("executor = %#v, want nil", exec)
		}
	})
}

func TestForgeExecuteRouteMountsForGitHubProvider(t *testing.T) {
	cfg := &config.Config{
		NATSURL: "",
		JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes",
		AllowedOrigins: []string{"http://localhost:3000"},
		NulangWorkloadID: "nulang-cloud",
		NulangWorkloadSecret: "0123456789abcdef0123456789abcdef",
		ForgeProvider: "github",
		GitHubForgeBaseURL: "https://api.github.test",
		GitHubForgeToken: "server-held-token",
	}
	s := New(cfg, nil, slog.Default())
	for _, path := range []string{
		"/api/v1/internal/forge/execute",
		"/api/v1/internal/forge/reconcile",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401 to prove route is mounted", path, rec.Code)
		}
	}
}
