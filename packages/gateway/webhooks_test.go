package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateGitHubWebhook(t *testing.T) {
	payload := []byte(`{"ref":"refs/heads/main"}`)
	secret := "webhook-secret"
	signature := ComputeGitHubWebhookSignature(payload, secret)

	if !ValidateGitHubWebhook(payload, signature, secret) {
		t.Fatal("expected valid signature")
	}
	if ValidateGitHubWebhook(payload, signature, "wrong-secret") {
		t.Fatal("expected wrong secret to fail")
	}
	if ValidateGitHubWebhook(payload, "", secret) {
		t.Fatal("expected empty signature to fail")
	}
	if ValidateGitHubWebhook(payload, signature, "") {
		t.Fatal("expected empty secret to fail")
	}
}

func TestParseGitHubWebhookRequiresSignatureWhenSecretConfigured(t *testing.T) {
	payload := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", GitHubEventPush)

	if _, err := ParseGitHubWebhook(req, "webhook-secret"); err == nil {
		t.Fatal("expected missing signature error")
	}
}

func TestParseGitHubWebhook(t *testing.T) {
	payload := []byte(`{
  "ref":"refs/heads/main",
  "repository":{"id":42,"full_name":"acme/app"},
  "sender":{"login":"octocat"}
}`)
	secret := "webhook-secret"
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", GitHubEventPush)
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-Hub-Signature-256", ComputeGitHubWebhookSignature(payload, secret))

	event, err := ParseGitHubWebhook(req, secret)
	if err != nil {
		t.Fatalf("parse webhook: %v", err)
	}
	if event.EventType != GitHubEventPush {
		t.Fatalf("event type = %q, want %s", event.EventType, GitHubEventPush)
	}
	if event.DeliveryID != "delivery-1" {
		t.Fatalf("delivery id = %q, want delivery-1", event.DeliveryID)
	}
	if event.RepositoryID != 42 {
		t.Fatalf("repository id = %d, want 42", event.RepositoryID)
	}
	if event.Repository != "acme/app" {
		t.Fatalf("repository = %q, want acme/app", event.Repository)
	}
	if event.Sender != "octocat" {
		t.Fatalf("sender = %q, want octocat", event.Sender)
	}

	ok, branch := IsBranchPush(event)
	if !ok || branch != "main" {
		t.Fatalf("branch push = (%v, %q), want (true, main)", ok, branch)
	}
}

func TestExtractIssueNumber(t *testing.T) {
	event := &WebhookEvent{
		EventType: GitHubEventIssues,
		Payload:   []byte(`{"issue":{"number":123}}`),
	}

	if got := ExtractIssueNumber(event); got != 123 {
		t.Fatalf("issue number = %d, want 123", got)
	}
}
