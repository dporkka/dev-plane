package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/gateway"
)

func TestGiteaWebhookRequiresDeliveryAndPublisher(t *testing.T) {
	const secret = "synthetic-secret"
	const valid = `{"repository":{"id":77,"full_name":"acme/widget"}}`
	for _, tc := range []struct {
		name, body, delivery string
		publisher            *webhookEventPublisher
		want                 int
	}{
		{name: "publisher unavailable", body: valid, delivery: "delivery-1", want: http.StatusServiceUnavailable},
		{name: "publisher failed", body: valid, delivery: "delivery-2", publisher: &webhookEventPublisher{err: errors.New("unavailable")}, want: http.StatusServiceUnavailable},
		{name: "missing delivery", body: valid, publisher: &webhookEventPublisher{}, want: http.StatusBadRequest},
		{name: "missing repository", body: `{"repository":{"id":77}}`, delivery: "delivery-3", publisher: &webhookEventPublisher{}, want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewGiteaWebhookHandler().WithWebhookSecret(secret)
			if tc.publisher != nil {
				h.WithEventPublisher(tc.publisher)
			}
			body := []byte(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/gitea", bytes.NewReader(body))
			req.Header.Set("X-Gitea-Event", "push")
			req.Header.Set("X-Gitea-Delivery", tc.delivery)
			req.Header.Set("X-Gitea-Signature", gateway.ComputeGiteaWebhookSignature(body, secret))
			rec := httptest.NewRecorder()
			h.GiteaWebhook(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusBadRequest && tc.publisher.subject != "" {
				t.Fatal("invalid event published")
			}
		})
	}
}
