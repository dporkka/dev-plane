package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
)

func TestGiteaIngressInstanceBinding(t *testing.T) {
	const secret = "test-signing-key"
	body := []byte("{  \"repository\": { \"id\":77, \"full_name\":\"acme/widget\", \"html_url\":\"https://git.example.test/code/acme/widget\" } }\n")
	for _, tc := range []struct {
		name, instance string
		want           int
	}{
		{"matching configured instance", "https://git.example.test/code/", http.StatusAccepted},
		{"missing instance", "", http.StatusServiceUnavailable},
		{"invalid instance", "https://git.example.test/code/api/v1", http.StatusServiceUnavailable},
		{"different instance", "https://other.example.test/code", http.StatusBadRequest},
		{"different subpath", "https://git.example.test/other", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pub := &webhookEventPublisher{}
			h := NewGiteaWebhookHandler().WithWebhookSecret(secret).WithInstanceURL(tc.instance).WithEventPublisher(pub)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/gitea", bytes.NewReader(body))
			req.Header.Set("X-Gitea-Event", "push")
			req.Header.Set("X-Gitea-Delivery", "fixture-delivery")
			req.Header.Set("X-Hub-Signature-256", gateway.ComputeGitHubWebhookSignature(body, secret))
			// Host, forwarding and external instance headers must not select the forge.
			req.Host = "attacker.example"
			req.Header.Set("X-Forwarded-Host", "attacker.example")
			req.Header.Set("X-Gitea-Instance", "https://attacker.example")
			rec := httptest.NewRecorder()
			h.GiteaWebhook(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != http.StatusAccepted {
				if pub.subject != "" {
					t.Fatal("unexpected publication")
				}
				return
			}
			var event events.WebhookEvent
			if err := json.Unmarshal(pub.data, &event); err != nil {
				t.Fatal(err)
			}
			if event.ForgeInstance != "https://git.example.test/code" || !bytes.Equal(event.SignedPayload, body) ||
				!gateway.ValidateGitHubWebhook(event.SignedPayload, event.Signature, secret) {
				t.Fatal("operator authority or exact signed bytes were lost")
			}
		})
	}
}
