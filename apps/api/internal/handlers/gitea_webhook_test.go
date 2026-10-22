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

func TestGiteaWebhookHandlerPublishesCanonicalEvent(t *testing.T) {
	publisher := &webhookEventPublisher{}
	secret := "gitea-secret"
	h := NewGiteaWebhookHandler().
		WithWebhookSecret(secret).
		WithEventPublisher(publisher)

	body := []byte(`{"ref":"refs/heads/main","repository":{"id":77,"full_name":"acme/widget"},"sender":{"login":"forge-bot"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Event", "push")
	req.Header.Set("X-Gitea-Delivery", "delivery-gitea-1")
	req.Header.Set("X-Gitea-Signature", gateway.ComputeGiteaWebhookSignature(body, secret))
	rec := httptest.NewRecorder()

	h.GiteaWebhook(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if publisher.subject != events.WebhookReceived {
		t.Fatalf("subject = %q, want %q", publisher.subject, events.WebhookReceived)
	}
	var event events.WebhookEvent
	if err := json.Unmarshal(publisher.data, &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Source != "gitea" || event.EventType != "push" || event.DeliveryID != "delivery-gitea-1" {
		t.Fatalf("event = %+v", event)
	}
	if event.RepositoryID != "acme/widget" {
		t.Fatalf("repository id = %q, want acme/widget", event.RepositoryID)
	}
}

func TestGiteaWebhookHandlerRejectsInvalidSignature(t *testing.T) {
	h := NewGiteaWebhookHandler().WithWebhookSecret("gitea-secret")
	body := []byte(`{"repository":{"id":77,"full_name":"acme/widget"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Event", "push")
	req.Header.Set("X-Gitea-Signature", gateway.ComputeGiteaWebhookSignature(body, "wrong-secret"))
	rec := httptest.NewRecorder()

	h.GiteaWebhook(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestGiteaWebhookHandlerRequiresConfiguredSecret(t *testing.T) {
	h := NewGiteaWebhookHandler()
	body := []byte(`{"repository":{"id":77,"full_name":"acme/widget"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Event", "push")
	rec := httptest.NewRecorder()

	h.GiteaWebhook(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestGiteaWebhookHandlerPingAcknowledgesWithoutPublishing(t *testing.T) {
	publisher := &webhookEventPublisher{}
	secret := "gitea-secret"
	h := NewGiteaWebhookHandler().WithWebhookSecret(secret).WithEventPublisher(publisher)
	body := []byte(`{"repository":{"id":77,"full_name":"acme/widget"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", bytes.NewReader(body))
	req.Header.Set("X-Gitea-Event", "ping")
	req.Header.Set("X-Gitea-Signature", gateway.ComputeGiteaWebhookSignature(body, secret))
	rec := httptest.NewRecorder()

	h.GiteaWebhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if publisher.subject != "" {
		t.Fatalf("unexpected published subject %q", publisher.subject)
	}
}
