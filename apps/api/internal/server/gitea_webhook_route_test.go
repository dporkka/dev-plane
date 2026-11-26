package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/config"
	"github.com/ai-dev-control-plane/gateway"
)

func TestGiteaWebhookRouteUsesConfiguredInstance(t *testing.T) {
	const secret = "configured-secret/"
	body := []byte(`{"repository":{"id":77,"full_name":"acme/widget","html_url":"https://git.example.test/code/acme/widget"}}`)
	for _, tc := range []struct {
		name, instance, eventType string
		want                      int
	}{
		{"configured signed ping", "https://git.example.test/code", "ping", http.StatusOK},
		{"disabled without instance", "", "ping", http.StatusServiceUnavailable},
		{"matching repository requires publisher", "https://git.example.test/code", "push", http.StatusServiceUnavailable},
		{"other instance rejected", "https://git.example.test/other", "push", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(&config.Config{GiteaWebhookSecret: secret, GiteaWebhookURL: tc.instance, JWTSecret: "01234567890123456789012345678901"}, nil, slog.Default())
			t.Cleanup(func() { _ = s.Close() })
			req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/gitea", bytes.NewReader(body))
			req.Header.Set("X-Gitea-Event", tc.eventType)
			req.Header.Set("X-Gitea-Delivery", "route-fixture")
			req.Header.Set("X-Gitea-Signature", gateway.ComputeGiteaWebhookSignature(body, secret))
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
