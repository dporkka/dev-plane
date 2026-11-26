package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
)

// GiteaWebhookHandler verifies Gitea webhook requests at the HTTP boundary and
// publishes the same canonical WebhookReceived envelope consumed by workers.
type GiteaWebhookHandler struct {
	eventBus      EventPublisher
	webhookSecret string
}

func NewGiteaWebhookHandler() *GiteaWebhookHandler {
	return &GiteaWebhookHandler{}
}

func (h *GiteaWebhookHandler) WithWebhookSecret(secret string) *GiteaWebhookHandler {
	h.webhookSecret = strings.TrimSpace(secret)
	return h
}

func (h *GiteaWebhookHandler) WithEventPublisher(pub EventPublisher) *GiteaWebhookHandler {
	h.eventBus = pub
	return h
}

func (h *GiteaWebhookHandler) GiteaWebhook(w http.ResponseWriter, r *http.Request) {
	if h.webhookSecret == "" {
		respond.Error(w, http.StatusServiceUnavailable, fmt.Errorf("gitea webhook secret is not configured"))
		return
	}

	event, err := gateway.ParseGiteaWebhook(r, h.webhookSecret)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, gateway.ErrForgeWebhookBodyTooLarge):
			status = http.StatusRequestEntityTooLarge
		case strings.Contains(strings.ToLower(err.Error()), "signature"):
			status = http.StatusUnauthorized
		}
		respond.Error(w, status, err)
		return
	}

	if event.EventType == "ping" {
		respond.JSON(w, http.StatusOK, map[string]string{"message": "pong"})
		return
	}

	if event.DeliveryID == "" || event.RepositoryID <= 0 || strings.TrimSpace(event.Repository) == "" {
		respond.Error(w, http.StatusBadRequest, fmt.Errorf("gitea webhook delivery and repository identity are required"))
		return
	}
	if h.eventBus == nil {
		respond.Error(w, http.StatusServiceUnavailable, fmt.Errorf("gitea webhook event publisher is not configured"))
		return
	}
	payload, err := json.Marshal(events.WebhookEvent{
		Source:       event.Source,
		EventType:    event.EventType,
		DeliveryID:   event.DeliveryID,
		RepositoryID: event.Repository,
		Payload:      event.Payload,
		Signature:    event.Signature,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("marshal gitea webhook event"))
		return
	}
	if err := h.eventBus.Publish(events.WebhookReceived, payload); err != nil {
		respond.Error(w, http.StatusServiceUnavailable, fmt.Errorf("publish gitea webhook event failed"))
		return
	}

	respond.JSON(w, http.StatusAccepted, map[string]string{
		"status":     "received",
		"event_type": event.EventType,
	})
}
