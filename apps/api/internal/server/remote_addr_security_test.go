package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/config"
)

func TestServerDoesNotTrustSpoofedForwardingHeaders(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	s := New(&config.Config{NATSURL: "", JWTSecret: "synthetic-test-secret-at-least-32-bytes"}, nil, logger)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "198.51.100.10:1234"
	req.Header.Set("True-Client-IP", "203.0.113.99")
	req.Header.Set("X-Real-IP", "203.0.113.99")
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	if !strings.Contains(logs.String(), "198.51.100.10:1234") || strings.Contains(logs.String(), "203.0.113.99") {
		t.Fatal("request log used untrusted proxy header rather than socket address")
	}
}
