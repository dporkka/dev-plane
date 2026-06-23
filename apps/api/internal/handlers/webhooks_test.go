package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/events"
)

func signPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidateGitHubWebhook_Valid(t *testing.T) {
	secret := "my-secret"
	payload := []byte(`{"ref":"refs/heads/main"}`)
	signature := signPayload(payload, secret)

	if !validateGitHubWebhook(payload, signature, secret) {
		t.Error("expected valid signature to be accepted")
	}
}

func TestValidateGitHubWebhook_Invalid(t *testing.T) {
	secret := "my-secret"
	payload := []byte(`{"ref":"refs/heads/main"}`)
	// Wrong signature format
	signature := "invalid-signature"

	if validateGitHubWebhook(payload, signature, secret) {
		t.Error("expected invalid signature to be rejected")
	}
}

func TestValidateGitHubWebhook_WrongSecret(t *testing.T) {
	secret := "my-secret"
	wrongSecret := "wrong-secret"
	payload := []byte(`{"ref":"refs/heads/main"}`)
	signature := signPayload(payload, secret)

	if validateGitHubWebhook(payload, signature, wrongSecret) {
		t.Error("expected signature with wrong secret to be rejected")
	}
}

func TestGitHubWebhookHandler_Push(t *testing.T) {
	publisher := &webhookEventPublisher{}
	secret := "my-secret"
	h := NewWebhookHandler().
		WithWebhookSecret(secret).
		WithEventPublisher(publisher)

	payload := map[string]interface{}{
		"ref": "refs/heads/main",
		"repository": map[string]string{
			"full_name": "acme/corp",
		},
	}
	body, _ := json.Marshal(payload)
	signature := signPayload(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "del-1")
	req.Header.Set("X-Hub-Signature-256", signature)
	rec := httptest.NewRecorder()

	h.GitHubWebhook(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["event_type"] != "push" {
		t.Errorf("expected event_type 'push', got %q", resp["event_type"])
	}

	if resp["status"] != "received" {
		t.Errorf("expected status 'received', got %q", resp["status"])
	}

	if publisher.subject != events.WebhookReceived {
		t.Fatalf("published subject = %q, want %s", publisher.subject, events.WebhookReceived)
	}
	var event events.WebhookEvent
	if err := json.Unmarshal(publisher.data, &event); err != nil {
		t.Fatalf("failed to unmarshal published event: %v", err)
	}
	if event.Source != "github" {
		t.Fatalf("published source = %q, want github", event.Source)
	}
	if event.EventType != "push" {
		t.Fatalf("published event type = %q, want push", event.EventType)
	}
	if event.DeliveryID != "del-1" {
		t.Fatalf("published delivery id = %q, want del-1", event.DeliveryID)
	}
	if event.RepositoryID != "acme/corp" {
		t.Fatalf("published repository = %q, want acme/corp", event.RepositoryID)
	}
}

func TestGitHubWebhookHandler_Issue(t *testing.T) {
	secret := "my-secret"
	h := NewWebhookHandler().WithWebhookSecret(secret)

	payload := map[string]interface{}{
		"action": "opened",
		"issue": map[string]interface{}{
			"number": 42,
			"title":  "Bug report",
			"body":   "Something is broken",
			"state":  "open",
		},
		"repository": map[string]string{
			"full_name": "acme/corp",
		},
	}
	body, _ := json.Marshal(payload)
	signature := signPayload(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "del-2")
	req.Header.Set("X-Hub-Signature-256", signature)
	rec := httptest.NewRecorder()

	h.GitHubWebhook(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["event_type"] != "issues" {
		t.Errorf("expected event_type 'issues', got %q", resp["event_type"])
	}

	if resp["status"] != "received" {
		t.Errorf("expected status 'received', got %q", resp["status"])
	}
}

func TestGitHubWebhookHandler_InvalidSignature(t *testing.T) {
	secret := "my-secret"
	h := NewWebhookHandler().WithWebhookSecret(secret)

	body := []byte(`{"ref":"refs/heads/main"}`)
	wrongSignature := signPayload(body, "wrong-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", wrongSignature)
	rec := httptest.NewRecorder()

	h.GitHubWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if resp["error"] == "" {
			t.Error("expected error message in response")
		}
	}
}

func TestGitHubWebhookHandler_MissingSignature(t *testing.T) {
	h := NewWebhookHandler().WithWebhookSecret("my-secret")

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	rec := httptest.NewRecorder()

	h.GitHubWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestGitHubWebhookHandler_MissingConfiguredSecret(t *testing.T) {
	h := NewWebhookHandler()

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signPayload(body, "my-secret"))
	rec := httptest.NewRecorder()

	h.GitHubWebhook(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

func TestGitHubWebhookHandler_PublishFailure(t *testing.T) {
	secret := "my-secret"
	h := NewWebhookHandler().
		WithWebhookSecret(secret).
		WithEventPublisher(&webhookEventPublisher{err: errors.New("nats unavailable")})

	body := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"acme/corp"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "del-1")
	req.Header.Set("X-Hub-Signature-256", signPayload(body, secret))
	rec := httptest.NewRecorder()

	h.GitHubWebhook(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

func TestGitHubWebhookHandler_MissingEventHeader(t *testing.T) {
	h := NewWebhookHandler()

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.GitHubWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

type webhookEventPublisher struct {
	subject string
	data    []byte
	err     error
}

func (p *webhookEventPublisher) Publish(subject string, data []byte) error {
	p.subject = subject
	p.data = append([]byte(nil), data...)
	return p.err
}
