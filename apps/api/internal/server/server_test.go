package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/config"
)

func TestNew(t *testing.T) {
	cfg := &config.Config{
		NATSURL:    "", // no NATS to avoid real connection
		JWTSecret:  "test-secret-that-is-at-least-thirty-two-bytes",
		Port:       "8080",
		LogLevel:   "info",
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
		NATSURL:   "",
		JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes",
		Port:      "8080",
		LogLevel:  "info",
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
		NATSURL:   "",
		JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes",
		Port:      "8080",
		LogLevel:  "info",
		AllowedOrigins: []string{"http://localhost:3000"},
	}
	s := New(cfg, nil, slog.Default())
	if err := s.Close(); err != nil {
		t.Fatalf("Close() with nil DB should not error: %v", err)
	}
}

func TestHealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		NATSURL:   "",
		JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes",
		Port:      "8080",
		LogLevel:  "info",
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
		NATSURL:   "",
		JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes",
		Port:      "8080",
		LogLevel:  "info",
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

func TestBuildForgeIntegration_Gitea(t *testing.T) {
	cfg := &config.Config{
		ForgeProvider:         "gitea",
		ForgeGitRemote:        "review",
		GiteaURL:              "https://code.example.com",
		GiteaToken:            "secret-token",
		GiteaUsername:         "alice",
		GiteaDraftTitlePrefix: "[Draft]",
	}

	integration, err := buildForgeIntegration(cfg, nil, slog.Default())
	if err != nil {
		t.Fatalf("buildForgeIntegration() error: %v", err)
	}
	if integration == nil {
		t.Fatal("integration is nil")
	}
	if integration.provider.Name() != "gitea" {
		t.Fatalf("provider = %q, want gitea", integration.provider.Name())
	}
	if integration.credential.Token != "secret-token" {
		t.Fatalf("credential token = %q", integration.credential.Token)
	}
	if integration.creator == nil {
		t.Fatal("creator is nil")
	}
}

func TestBuildForgeIntegration_ForgejoAlias(t *testing.T) {
	cfg := &config.Config{
		ForgeProvider:  "forgejo",
		GiteaURL:       "https://forge.example.com",
		GiteaToken:     "secret-token",
		GiteaUsername:  "alice",
		ForgeGitRemote: "origin",
	}
	integration, err := buildForgeIntegration(cfg, nil, slog.Default())
	if err != nil {
		t.Fatalf("buildForgeIntegration() error: %v", err)
	}
	if integration == nil || integration.provider.Name() != "gitea" {
		t.Fatalf("integration = %#v, want Gitea-compatible provider", integration)
	}
}

func TestBuildForgeIntegration_GiteaRequiresURL(t *testing.T) {
	cfg := &config.Config{ForgeProvider: "gitea"}
	if _, err := buildForgeIntegration(cfg, nil, slog.Default()); err == nil {
		t.Fatal("expected missing Gitea URL error")
	}
}

func TestBuildForgeIntegration_RejectsUnknownProvider(t *testing.T) {
	cfg := &config.Config{ForgeProvider: "bitbucket"}
	if _, err := buildForgeIntegration(cfg, nil, slog.Default()); err == nil {
		t.Fatal("expected unsupported forge provider error")
	}
}
