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
