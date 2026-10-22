package gateway

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateGiteaWebhook(t *testing.T) {
	payload := []byte(`{"ref":"refs/heads/main"}`)
	secret := "webhook-secret"
	signature := ComputeGiteaWebhookSignature(payload, secret)

	if !ValidateGiteaWebhook(payload, signature, secret) {
		t.Fatal("expected valid signature")
	}
	if ValidateGiteaWebhook(payload, signature, "wrong-secret") {
		t.Fatal("expected wrong secret to fail")
	}
	if ValidateGiteaWebhook(payload, "", secret) {
		t.Fatal("expected empty signature to fail")
	}
}

func TestParseGiteaWebhookNormalizesCanonicalFields(t *testing.T) {
	payload := []byte(`{
  "ref":"refs/heads/main",
  "repository":{"id":77,"full_name":"acme/widget"},
  "sender":{"login":"forge-bot"}
}`)
	secret := "webhook-secret"
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", bytes.NewReader(payload))
	req.Header.Set("X-Gitea-Event", "push")
	req.Header.Set("X-Gitea-Delivery", "delivery-gitea-1")
	req.Header.Set("X-Gitea-Signature", ComputeGiteaWebhookSignature(payload, secret))

	event, err := ParseGiteaWebhook(req, secret)
	if err != nil {
		t.Fatalf("parse gitea webhook: %v", err)
	}
	if event.Source != "gitea" || event.EventType != "push" || event.DeliveryID != "delivery-gitea-1" {
		t.Fatalf("event identity = %+v", event)
	}
	if event.RepositoryID != 77 || event.Repository != "acme/widget" || event.Sender != "forge-bot" {
		t.Fatalf("event repository metadata = %+v", event)
	}
	if ok, branch := IsBranchPush(event); !ok || branch != "main" {
		t.Fatalf("branch push = (%v, %q), want (true, main)", ok, branch)
	}
}

func TestParseGiteaWebhookPrefersSpecificEventType(t *testing.T) {
	payload := []byte(`{"repository":{"id":77,"full_name":"acme/widget"}}`)
	secret := "webhook-secret"
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", bytes.NewReader(payload))
	req.Header.Set("X-Gitea-Event", "pull_request")
	req.Header.Set("X-Gitea-Event-Type", "pull_request_review_approved")
	req.Header.Set("X-Gitea-Signature", ComputeGiteaWebhookSignature(payload, secret))

	event, err := ParseGiteaWebhook(req, secret)
	if err != nil {
		t.Fatalf("parse gitea webhook: %v", err)
	}
	if event.EventType != "pull_request_review_approved" {
		t.Fatalf("event type = %q, want specific X-Gitea-Event-Type", event.EventType)
	}
}

func TestParseGiteaWebhookRejectsSignedMalformedJSON(t *testing.T) {
	payload := []byte(`{"repository":`)
	secret := "webhook-secret"
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", bytes.NewReader(payload))
	req.Header.Set("X-Gitea-Event", "push")
	req.Header.Set("X-Gitea-Signature", ComputeGiteaWebhookSignature(payload, secret))

	if _, err := ParseGiteaWebhook(req, secret); err == nil {
		t.Fatal("expected malformed signed payload to fail")
	}
}

func TestParseGiteaWebhookAcceptsDocumentedHubSignatureFallback(t *testing.T) {
	payload := []byte(`{"repository":{"id":77,"full_name":"acme/widget"}}`)
	secret := "webhook-secret"
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", bytes.NewReader(payload))
	req.Header.Set("X-Gitea-Event", "issues")
	req.Header.Set("X-Gitea-Delivery", "delivery-gitea-2")
	req.Header.Set("X-Hub-Signature-256", ComputeGitHubWebhookSignature(payload, secret))

	if _, err := ParseGiteaWebhook(req, secret); err != nil {
		t.Fatalf("parse gitea webhook with hub signature: %v", err)
	}
}

func TestParseGiteaWebhookRejectsMissingSignatureWhenSecretConfigured(t *testing.T) {
	payload := []byte(`{"repository":{"id":77}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", bytes.NewReader(payload))
	req.Header.Set("X-Gitea-Event", "push")
	if _, err := ParseGiteaWebhook(req, "webhook-secret"); err == nil {
		t.Fatal("expected missing signature error")
	}
}

func TestParseForgeWebhookDispatchesByProvider(t *testing.T) {
	payload := []byte(`{"repository":{"id":77,"full_name":"acme/widget"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/forge", bytes.NewReader(payload))
	req.Header.Set("X-Gitea-Event", "push")

	event, err := ParseForgeWebhook("gitea", req, "")
	if err != nil {
		t.Fatalf("parse forge webhook: %v", err)
	}
	if event.Source != "gitea" {
		t.Fatalf("source = %q, want gitea", event.Source)
	}
}

func TestParseForgeWebhookRejectsUnknownProviderBeforeReadingBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/forge", bytes.NewReader([]byte(`{}`)))
	_, err := ParseForgeWebhook("mystery", req, "")
	if !errors.Is(err, ErrUnsupportedForgeProvider) {
		t.Fatalf("error = %v, want ErrUnsupportedForgeProvider", err)
	}
}
