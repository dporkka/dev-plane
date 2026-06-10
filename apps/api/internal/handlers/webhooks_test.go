package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

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
	h := NewWebhookHandler().WithEventPublisher(publisher)

	payload := map[string]interface{}{
		"ref": "refs/heads/main",
		"repository": map[string]string{
			"full_name": "acme/corp",
		},
	}
	body, _ := json.Marshal(payload)
	secret := "my-secret"
	signature := signPayload(body, secret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github?secret="+secret, bytes.NewReader(body))
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
	h := NewWebhookHandler()

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

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "del-2")
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
	h := NewWebhookHandler()

	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "my-secret"
	wrongSignature := signPayload(body, "wrong-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github?secret="+secret, bytes.NewReader(body))
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

func TestGitHubWebhookHandler_PublishFailure(t *testing.T) {
	h := NewWebhookHandler().WithEventPublisher(&webhookEventPublisher{err: errors.New("nats unavailable")})

	body := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"acme/corp"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "del-1")
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

func TestIntegrationWebhook_LinearCreatesTask(t *testing.T) {
	baseHandler, mock, cleanup := setupTest(t)
	defer cleanup()

	configJSON := `{"project_id":"proj-1","repository_id":"repo-1","created_by":"user-1","webhook_secret":"linear-secret"}`
	mock.ExpectQuery("SELECT id, organization_id, integration_type, display_name, config, status FROM integrations").
		WithArgs("int-1", integrationTypeLinear).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "integration_type", "display_name", "config", "status"}).
			AddRow("int-1", "org-1", integrationTypeLinear, "Linear", configJSON, "connected"))
	mock.ExpectQuery("SELECT id FROM tasks").
		WithArgs(integrationTypeLinear, "lin-123").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO tasks").
		WithArgs(sqlmock.AnyArg(), "proj-1", "repo-1", "user-1", integrationTypeLinear, "lin-123", "Fix production bug", "Investigate the failing deploy", "medium", "low", "main", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE integrations SET last_synced_at =").
		WithArgs(sqlmock.AnyArg(), "int-1").
		WillReturnResult(sqlmock.NewResult(1, 1))

	h := NewWebhookHandler().WithHandler(baseHandler)
	body := []byte(`{"type":"Issue","action":"create","data":{"id":"lin-123","identifier":"ENG-42","title":"Fix production bug","description":"Investigate the failing deploy","state":{"name":"Todo"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear/int-1", bytes.NewReader(body))
	req.Header.Set("X-Linear-Signature", signPayload(body, "linear-secret"))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", integrationTypeLinear)
	rctx.URLParams.Add("integrationID", "int-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.IntegrationWebhook(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled expectations: %v", err)
	}
}

func TestIntegrationWebhook_SlackCreateCommand(t *testing.T) {
	baseHandler, mock, cleanup := setupTest(t)
	defer cleanup()

	configJSON := `{"project_id":"proj-1","repository_id":"repo-1","created_by":"user-1","webhook_secret":"slack-secret"}`
	mock.ExpectQuery("SELECT id, organization_id, integration_type, display_name, config, status FROM integrations").
		WithArgs("int-2", integrationTypeSlack).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "integration_type", "display_name", "config", "status"}).
			AddRow("int-2", "org-1", integrationTypeSlack, "Slack", configJSON, "connected"))
	mock.ExpectExec("INSERT INTO tasks").
		WithArgs(sqlmock.AnyArg(), "proj-1", "repo-1", "user-1", integrationTypeSlack, sqlmock.AnyArg(), "Document the new integration flow", sqlmock.AnyArg(), "medium", "low", "main", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE integrations SET last_synced_at =").
		WithArgs(sqlmock.AnyArg(), "int-2").
		WillReturnResult(sqlmock.NewResult(1, 1))

	h := NewWebhookHandler().WithHandler(baseHandler)
	body := []byte(`{"text":"create Document the new integration flow"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/slack/int-2", bytes.NewReader(body))
	req.Header.Set("X-Slack-Signature", signPayload(body, "slack-secret"))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", integrationTypeSlack)
	rctx.URLParams.Add("integrationID", "int-2")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.IntegrationWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled expectations: %v", err)
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
