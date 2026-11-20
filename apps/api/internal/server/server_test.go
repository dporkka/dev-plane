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

// Untrusted forwarded headers must not overwrite the transport peer address.
// Trust decisions belong at the configured ingress/proxy boundary, not in
// chi's unconditional RealIP middleware.
func TestUntrustedProxyHeadersCannotSpoofRemoteAddr(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes", AllowedOrigins: []string{"http://localhost:3000"}}
	s := New(cfg, nil, slog.Default())
	s.router.Get("/test/remote-address", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.RemoteAddr)) })
	req := httptest.NewRequest(http.MethodGet, "/test/remote-address", nil)
	req.RemoteAddr = "10.0.0.9:4567"
	req.Header.Set("X-Forwarded-For", "198.51.100.4")
	req.Header.Set("True-Client-IP", "192.0.2.5")
	req.Header.Set("X-Real-IP", "203.0.113.6")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Body.String() != req.RemoteAddr {
		t.Fatalf("remote address was spoofed: got %q want %q", rec.Body.String(), req.RemoteAddr)
	}
}
