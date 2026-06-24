package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/events"
)

// WebhookHandler handles incoming webhooks from GitHub.
type WebhookHandler struct {
	eventBus      EventPublisher
	webhookSecret string
}

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler() *WebhookHandler {
	return &WebhookHandler{}
}

// WithEventPublisher adds an event publisher for accepted webhook events.
func (h *WebhookHandler) WithEventPublisher(pub EventPublisher) *WebhookHandler {
	h.eventBus = pub
	return h
}

// WithWebhookSecret configures the shared GitHub webhook signing secret.
func (h *WebhookHandler) WithWebhookSecret(secret string) *WebhookHandler {
	h.webhookSecret = strings.TrimSpace(secret)
	return h
}

// GitHubEvent represents a parsed GitHub webhook event.
type GitHubEvent struct {
	EventType  string          `json:"event_type"`
	DeliveryID string          `json:"delivery_id"`
	Repository string          `json:"repository,omitempty"`
	Action     string          `json:"action,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

// GitHubWebhook handles incoming GitHub webhook events.
func (h *WebhookHandler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	signature := r.Header.Get("X-Hub-Signature-256")

	if eventType == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("missing X-GitHub-Event header"))
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("failed to read body"))
		return
	}

	if h.webhookSecret == "" {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("github webhook secret is not configured"))
		return
	}
	if signature == "" {
		respond.Error(w, http.StatusUnauthorized, errors.New("missing X-Hub-Signature-256 header"))
		return
	}
	if !validateGitHubWebhook(body, signature, h.webhookSecret) {
		respond.Error(w, http.StatusUnauthorized, errors.New("invalid webhook signature"))
		return
	}

	event := GitHubEvent{
		EventType:  eventType,
		DeliveryID: deliveryID,
		Payload:    body,
	}

	switch eventType {
	case "push":
		var payload struct {
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			Ref string `json:"ref"`
		}
		if err := json.Unmarshal(body, &payload); err == nil {
			event.Repository = payload.Repository.FullName
		}

	case "pull_request":
		var payload struct {
			Action     string `json:"action"`
			Number     int    `json:"number"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			PullRequest struct {
				HTMLURL string `json:"html_url"`
				State   string `json:"state"`
				Title   string `json:"title"`
			} `json:"pull_request"`
		}
		if err := json.Unmarshal(body, &payload); err == nil {
			event.Action = payload.Action
			event.Repository = payload.Repository.FullName
		}

	case "issues":
		var payload struct {
			Action string `json:"action"`
			Issue  struct {
				Number int    `json:"number"`
				Title  string `json:"title"`
				Body   string `json:"body"`
				State  string `json:"state"`
			} `json:"issue"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &payload); err == nil {
			event.Action = payload.Action
			event.Repository = payload.Repository.FullName
		}

	case "ping":
		respond.JSON(w, http.StatusOK, map[string]string{"message": "pong"})
		return

	default:
		// Acknowledge unhandled events
	}

	if err := h.publishReceivedWebhook(event, signature); err != nil {
		respond.Error(w, http.StatusServiceUnavailable, err)
		return
	}

	respond.JSON(w, http.StatusAccepted, map[string]string{
		"status":     "received",
		"event_type": eventType,
	})
}

// LinearWebhook handles incoming Linear webhook events.
func (h *WebhookHandler) LinearWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("failed to read body"))
		return
	}

	eventType := r.Header.Get("linear-event")
	if eventType == "" {
		eventType = "unknown"
	}

	if err := h.publishWebhookEvent("linear", GitHubEvent{
		EventType:  eventType,
		DeliveryID: r.Header.Get("linear-delivery-id"),
		Payload:    body,
	}, ""); err != nil {
		respond.Error(w, http.StatusServiceUnavailable, err)
		return
	}

	respond.JSON(w, http.StatusAccepted, map[string]string{
		"status":     "received",
		"event_type": eventType,
	})
}

// SlackWebhook handles incoming Slack webhook events.
func (h *WebhookHandler) SlackWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("failed to read body"))
		return
	}

	// Slack URL verification handshake for event subscriptions
	var payload struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Type == "url_verification" {
		respond.JSON(w, http.StatusOK, map[string]string{"challenge": payload.Challenge})
		return
	}

	eventType := r.Header.Get("X-Slack-Event")
	if eventType == "" {
		eventType = "event_callback"
	}

	if err := h.publishWebhookEvent("slack", GitHubEvent{
		EventType:  eventType,
		DeliveryID: r.Header.Get("X-Slack-Retry-Num"),
		Payload:    body,
	}, ""); err != nil {
		respond.Error(w, http.StatusServiceUnavailable, err)
		return
	}

	respond.JSON(w, http.StatusAccepted, map[string]string{
		"status":     "received",
		"event_type": eventType,
	})
}

// DiscordWebhook handles incoming Discord webhook events.
func (h *WebhookHandler) DiscordWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("failed to read body"))
		return
	}

	if err := h.publishWebhookEvent("discord", GitHubEvent{
		EventType:  "event",
		DeliveryID: r.Header.Get("X-Discord-Event-Id"),
		Payload:    body,
	}, ""); err != nil {
		respond.Error(w, http.StatusServiceUnavailable, err)
		return
	}

	respond.JSON(w, http.StatusAccepted, map[string]string{
		"status":     "received",
		"event_type": "event",
	})
}

func (h *WebhookHandler) publishReceivedWebhook(event GitHubEvent, signature string) error {
	return h.publishWebhookEvent("github", event, signature)
}

func (h *WebhookHandler) publishWebhookEvent(source string, event GitHubEvent, signature string) error {
	if h.eventBus == nil {
		return nil
	}

	payload, err := json.Marshal(events.WebhookEvent{
		Source:       source,
		EventType:    event.EventType,
		DeliveryID:   event.DeliveryID,
		RepositoryID: event.Repository,
		Payload:      event.Payload,
		Signature:    signature,
	})
	if err != nil {
		return fmt.Errorf("marshal webhook event: %w", err)
	}
	if err := h.eventBus.Publish(events.WebhookReceived, payload); err != nil {
		return fmt.Errorf("publish webhook event: %w", err)
	}
	return nil
}

// validateGitHubWebhook validates the HMAC-SHA256 signature of a GitHub webhook payload.
func validateGitHubWebhook(payload []byte, signature, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	sigHex := strings.TrimPrefix(signature, "sha256=")
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)

	return hmac.Equal(sigBytes, expected)
}
