package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/config"
)

// Browser-job routes are registered inside existing JWT authentication.
func TestBrowserJobRoutesRequireJWT(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes", AllowedOrigins: []string{"http://localhost:3000"}}
	srv := New(cfg, nil, slog.Default())
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/organizations/org-a/browser-jobs"},
		{http.MethodGet, "/api/v1/organizations/org-a/browser-jobs/job-a"},
	} {
		req := httptest.NewRequest(endpoint.method, endpoint.path, nil)
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s got %d; expected JWT rejection", endpoint.method, endpoint.path, res.Code)
		}
	}
}

// Untrusted forwarded IP headers must not replace the network peer address.
func TestBrowserJobRoutesDoNotTrustForwardedFor(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test-secret-that-is-at-least-thirty-two-bytes", AllowedOrigins: []string{"http://localhost:3000"}}
	srv := New(cfg, nil, slog.Default())
	srv.router.Get("/_test/remote-addr", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.RemoteAddr)) })
	req := httptest.NewRequest(http.MethodGet, "/_test/remote-addr", nil)
	req.RemoteAddr = "203.0.113.10:3400"
	req.Header.Set("X-Forwarded-For", "198.51.100.70")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if got := res.Body.String(); got != "203.0.113.10:3400" {
		t.Fatalf("trusted arbitrary forwarded IP: %s", got)
	}
}
