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


func TestUntrustedForwardedIPDoesNotOverridePeer(t *testing.T) {
    cfg := &config.Config{
        JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes",
        AllowedOrigins: []string{"http://localhost:3000"},
    }
    srv := New(cfg, nil, slog.Default())
    srv.router.Get("/test-peer-ip", func(w http.ResponseWriter, r *http.Request) {
        _, _ = w.Write([]byte(r.RemoteAddr))
    })
    request := httptest.NewRequest(http.MethodGet, "/test-peer-ip", nil)
    request.RemoteAddr = "203.0.113.10:50123"
    request.Header.Set("X-Forwarded-For", "198.51.100.123")
    request.Header.Set("X-Real-IP", "198.51.100.124")
    recorder := httptest.NewRecorder()
    srv.Handler().ServeHTTP(recorder, request)
    if recorder.Code != http.StatusOK {
        t.Fatalf("status = %d, want 200", recorder.Code)
    }
    if recorder.Body.String() != request.RemoteAddr {
        t.Fatalf("peer IP was overwritten by untrusted forwarded headers: %q", recorder.Body.String())
    }
}
