package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/events"
)

// WebhookHandler handles incoming webhooks from GitHub, Linear, Slack, Discord,
// and other provider integrations.
type WebhookHandler struct {
	eventBus             EventPublisher
	handler              *Handler
	webhookSecret        string
	linearWebhookSecret  string
	slackSigningSecret   string
	discordWebhookSecret string
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

// WithLinearWebhookSecret configures the Linear webhook signing secret.
func (h *WebhookHandler) WithLinearWebhookSecret(secret string) *WebhookHandler {
	h.linearWebhookSecret = strings.TrimSpace(secret)
	return h
}

// WithSlackSigningSecret configures the Slack request signing secret.
func (h *WebhookHandler) WithSlackSigningSecret(secret string) *WebhookHandler {
	h.slackSigningSecret = strings.TrimSpace(secret)
	return h
}

// WithDiscordWebhookSecret configures the shared secret used to authorize Discord webhook requests.
func (h *WebhookHandler) WithDiscordWebhookSecret(secret string) *WebhookHandler {
	h.discordWebhookSecret = strings.TrimSpace(secret)
	return h
}

// WithHandler adds access to shared application services for generic providers.
func (h *WebhookHandler) WithHandler(handler *Handler) *WebhookHandler {
	h.handler = handler
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

type genericWebhookIntegration struct {
	ID              string
	OrganizationID  string
	IntegrationType string
	DisplayName     string
	Config          json.RawMessage
	Status          string
}

const sourceIDSeparator = ":"

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

// IntegrationWebhook handles provider-specific webhook ingestion for non-GitHub integrations.
func (h *WebhookHandler) IntegrationWebhook(w http.ResponseWriter, r *http.Request) {
	if h.handler == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("integration handler unavailable"))
		return
	}

	ctx := r.Context()
	provider := chi.URLParam(r, "provider")
	integrationID := chi.URLParam(r, "integrationID")
	if provider == "" || integrationID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("provider and integration id are required"))
		return
	}

	integration, err := h.lookupIntegration(ctx, integrationID, provider)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("integration not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	cfg, err := parseIntegrationConfig(integration.Config)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid integration config"))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("failed to read body"))
		return
	}

	signature := webhookSignatureFromRequest(r)
	if cfg.WebhookSecret != "" {
		if signature == "" {
			respond.Error(w, http.StatusUnauthorized, errors.New("missing webhook signature"))
			return
		}
		switch provider {
		case integrationTypeSlack:
			timestamp := r.Header.Get("X-Slack-Request-Timestamp")
			if timestamp == "" || !validateSlackWebhook(body, signature, timestamp, cfg.WebhookSecret) {
				respond.Error(w, http.StatusUnauthorized, errors.New("invalid slack webhook signature"))
				return
			}
		default:
			if !validateGenericWebhook(body, signature, cfg.WebhookSecret) {
				respond.Error(w, http.StatusUnauthorized, errors.New("invalid webhook signature"))
				return
			}
		}
	}

	eventType := provider
	deliveryID := r.Header.Get("X-Request-ID")
	if deliveryID == "" {
		deliveryID = r.Header.Get("X-Linear-Delivery")
	}
	switch provider {
	case integrationTypeLinear:
		eventType = extractLinearEventType(body)
	case integrationTypeSlack, integrationTypeDiscord:
		eventType = "command"
	}

	if err := h.publishGenericWebhook(provider, eventType, deliveryID, integrationID, body, signature); err != nil {
		respond.Error(w, http.StatusServiceUnavailable, err)
		return
	}

	switch provider {
	case integrationTypeLinear:
		task, created, err := h.handleLinearEvent(ctx, integration, cfg, body)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, err)
			return
		}
		respond.JSON(w, http.StatusAccepted, map[string]any{
			"status":      "received",
			"provider":    provider,
			"event_type":  eventType,
			"created":     created,
			"task_id":     task.ID,
			"integration": integration.ID,
		})
	case integrationTypeSlack, integrationTypeDiscord:
		response, err := h.handleCommandEvent(ctx, provider, integration, cfg, body)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, err)
			return
		}
		respond.JSON(w, http.StatusOK, response)
	case integrationTypeWebhook:
		task, err := h.handleGenericWebhookTask(ctx, provider, integration, cfg, body)
		if err != nil {
			respond.Error(w, http.StatusBadRequest, err)
			return
		}
		respond.JSON(w, http.StatusAccepted, map[string]any{
			"status":      "received",
			"provider":    provider,
			"task_id":     task.ID,
			"integration": integration.ID,
		})
	default:
		respond.JSON(w, http.StatusAccepted, map[string]any{
			"status":      "received",
			"provider":    provider,
			"integration": integration.ID,
		})
	}
}

// LinearWebhook handles incoming Linear webhook events using the global signing secret.
func (h *WebhookHandler) LinearWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("failed to read body"))
		return
	}

	if h.linearWebhookSecret == "" {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("linear webhook secret is not configured"))
		return
	}
	signature := r.Header.Get("linear-signature")
	if signature == "" {
		respond.Error(w, http.StatusUnauthorized, errors.New("missing linear-signature header"))
		return
	}
	if !validateLinearWebhook(body, signature, h.linearWebhookSecret) {
		respond.Error(w, http.StatusUnauthorized, errors.New("invalid linear webhook signature"))
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

// SlackWebhook handles incoming Slack webhook events using the global signing secret.
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

	if h.slackSigningSecret == "" {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("slack signing secret is not configured"))
		return
	}
	signature := r.Header.Get("X-Slack-Signature")
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	if signature == "" || timestamp == "" {
		respond.Error(w, http.StatusUnauthorized, errors.New("missing slack signature headers"))
		return
	}
	if !validateSlackWebhook(body, signature, timestamp, h.slackSigningSecret) {
		respond.Error(w, http.StatusUnauthorized, errors.New("invalid slack webhook signature"))
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

// DiscordWebhook handles incoming Discord webhook events using the global authorization secret.
func (h *WebhookHandler) DiscordWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("failed to read body"))
		return
	}

	if h.discordWebhookSecret == "" {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("discord webhook secret is not configured"))
		return
	}
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		respond.Error(w, http.StatusUnauthorized, errors.New("missing discord authorization header"))
		return
	}
	if strings.TrimPrefix(authHeader, "Bearer ") != h.discordWebhookSecret {
		respond.Error(w, http.StatusUnauthorized, errors.New("invalid discord webhook secret"))
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

func (h *WebhookHandler) publishGenericWebhook(source, eventType, deliveryID, repositoryID string, body []byte, signature string) error {
	if h.eventBus == nil {
		return nil
	}

	payload, err := json.Marshal(events.WebhookEvent{
		Source:       source,
		EventType:    eventType,
		DeliveryID:   deliveryID,
		RepositoryID: repositoryID,
		Payload:      body,
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

func (h *WebhookHandler) lookupIntegration(ctx context.Context, integrationID, provider string) (genericWebhookIntegration, error) {
	var integration genericWebhookIntegration
	var cfg sql.NullString
	err := h.handler.db.QueryRowContext(ctx, `
		SELECT id, organization_id, integration_type, display_name, config, status
		FROM integrations
		WHERE id = $1 AND integration_type = $2 AND deleted_at IS NULL
	`, integrationID, provider).Scan(&integration.ID, &integration.OrganizationID, &integration.IntegrationType, &integration.DisplayName, &cfg, &integration.Status)
	if err != nil {
		return genericWebhookIntegration{}, err
	}
	if cfg.Valid {
		integration.Config = json.RawMessage(cfg.String)
	}
	return integration, nil
}

type linearIssueEvent struct {
	Action string `json:"action"`
	Type   string `json:"type"`
	Data   struct {
		ID          string `json:"id"`
		Identifier  string `json:"identifier"`
		Title       string `json:"title"`
		Description string `json:"description"`
		State       struct {
			Name string `json:"name"`
		} `json:"state"`
	} `json:"data"`
}

func extractLinearEventType(body []byte) string {
	var payload linearIssueEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return "linear"
	}
	if payload.Type != "" {
		return payload.Type
	}
	return "linear"
}

func (h *WebhookHandler) handleLinearEvent(ctx context.Context, integration genericWebhookIntegration, cfg integrationRuntimeConfig, body []byte) (Task, bool, error) {
	if cfg.ProjectID == "" || cfg.RepositoryID == "" || cfg.CreatedBy == "" {
		return Task{}, false, errors.New("integration config requires project_id, repository_id, and created_by")
	}

	var payload linearIssueEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return Task{}, false, errors.New("invalid linear webhook payload")
	}
	if payload.Data.ID == "" {
		return Task{}, false, errors.New("linear issue id is required")
	}

	metadata, _ := json.Marshal(map[string]any{
		"integration_id": integration.ID,
		"provider":       integrationTypeLinear,
		"identifier":     payload.Data.Identifier,
		"state":          payload.Data.State.Name,
	})

	title := strings.TrimSpace(payload.Data.Title)
	if title == "" {
		title = payload.Data.Identifier
	}
	if title == "" {
		title = "Linear task"
	}

	var existingID string
	err := h.handler.db.QueryRowContext(ctx, `
			SELECT id FROM tasks
			WHERE source = $1 AND source_id = $2 AND deleted_at IS NULL
		`, integrationTypeLinear, payload.Data.ID).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, err
	}

	if errors.Is(err, sql.ErrNoRows) {
		task, insertErr := h.handler.insertTask(ctx, createTaskOptions{
			ProjectID:    cfg.ProjectID,
			RepositoryID: cfg.RepositoryID,
			CreatedBy:    cfg.CreatedBy,
			Source:       integrationTypeLinear,
			SourceID:     &payload.Data.ID,
			Title:        title,
			Description:  payload.Data.Description,
			Priority:     cfg.DefaultPriority,
			RiskLevel:    cfg.DefaultRiskLevel,
			TargetBranch: cfg.TargetBranch,
			Metadata:     metadata,
		})
		if insertErr != nil {
			return Task{}, false, insertErr
		}
		h.touchIntegrationSync(ctx, integration.ID)
		return task, true, nil
	}

	result, updateErr := h.handler.db.ExecContext(ctx, `
			UPDATE tasks
			SET title = COALESCE($1, title),
			    description = COALESCE($2, description),
			    updated_at = $3,
			    metadata = $4
			WHERE id = $5 AND deleted_at IS NULL
		`, title, nullableString(payload.Data.Description), time.Now().UTC(), metadata, existingID)
	if updateErr != nil {
		return Task{}, false, updateErr
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Task{}, false, errors.New("linked task not found")
	}

	var task Task
	if err := scanTask(h.handler.db.QueryRowContext(ctx, `
			SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id,
			       title, description, status, priority, risk_level, target_branch,
			       spec, acceptance_criteria, max_cost, max_runtime_minutes,
			       approval_requirements, metadata, started_at, completed_at, created_at, updated_at
			FROM tasks
			WHERE id = $1
		`, existingID), &task); err != nil {
		return Task{}, false, err
	}
	h.touchIntegrationSync(ctx, integration.ID)
	return task, false, nil
}

type commandPayload struct {
	Text string `json:"text"`
}

func (h *WebhookHandler) handleCommandEvent(ctx context.Context, provider string, integration genericWebhookIntegration, cfg integrationRuntimeConfig, body []byte) (map[string]any, error) {
	if cfg.ProjectID == "" || cfg.RepositoryID == "" || cfg.CreatedBy == "" {
		return nil, errors.New("integration config requires project_id, repository_id, and created_by")
	}

	var payload commandPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("invalid command payload")
	}
	command := strings.TrimSpace(payload.Text)
	if command == "" {
		command = "help"
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		parts = []string{"help"}
	}

	switch strings.ToLower(parts[0]) {
	case "help":
		return map[string]any{
			"provider": provider,
			"message":  "Commands: help, create <title>, show <task-id>, status <task-id>, approve <approval-id> [note], reject <approval-id> [note]",
		}, nil
	case "create":
		title := strings.TrimSpace(strings.TrimPrefix(command, parts[0]))
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, errors.New("create command requires a task title")
		}
		sourceID := generateSourceID(provider)
		metadata, _ := json.Marshal(map[string]any{
			"integration_id": integration.ID,
			"provider":       provider,
			"command":        command,
		})
		task, err := h.handler.insertTask(ctx, createTaskOptions{
			ProjectID:    cfg.ProjectID,
			RepositoryID: cfg.RepositoryID,
			CreatedBy:    cfg.CreatedBy,
			Source:       integrationSourceForProvider(provider),
			SourceID:     &sourceID,
			Title:        title,
			Priority:     cfg.DefaultPriority,
			RiskLevel:    cfg.DefaultRiskLevel,
			TargetBranch: cfg.TargetBranch,
			Metadata:     metadata,
		})
		if err != nil {
			return nil, err
		}
		h.touchIntegrationSync(ctx, integration.ID)
		return map[string]any{"provider": provider, "message": "task created", "task": task}, nil
	case "show", "status":
		if len(parts) < 2 {
			return nil, errors.New("show/status command requires a task id")
		}
		var task Task
		if err := scanTask(h.handler.db.QueryRowContext(ctx, `
				SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id,
				       title, description, status, priority, risk_level, target_branch,
				       spec, acceptance_criteria, max_cost, max_runtime_minutes,
				       approval_requirements, metadata, started_at, completed_at, created_at, updated_at
				FROM tasks
				WHERE id = $1 AND deleted_at IS NULL
			`, parts[1]), &task); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errors.New("task not found")
			}
			return nil, err
		}
		return map[string]any{"provider": provider, "task": task}, nil
	case "approve", "reject":
		if len(parts) < 2 {
			return nil, errors.New("approve/reject command requires an approval id")
		}
		response := "approved"
		if strings.ToLower(parts[0]) == "reject" {
			response = "rejected"
		}
		note := strings.TrimSpace(strings.TrimPrefix(command, parts[0]+" "+parts[1]))
		now := time.Now().UTC()
		result, err := h.handler.db.ExecContext(ctx, `
				UPDATE approvals
				SET response = $1,
				    response_note = $2,
				    responded_by = $3,
				    responded_at = $4,
				    updated_at = $4
				WHERE id = $5
			`, response, nullableString(note), cfg.CreatedBy, now, parts[1])
		if err != nil {
			return nil, err
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return nil, errors.New("approval not found")
		}
		h.touchIntegrationSync(ctx, integration.ID)
		return map[string]any{"provider": provider, "message": "approval updated", "response": response, "approval_id": parts[1]}, nil
	default:
		return nil, errors.New("unsupported command")
	}
}

func (h *WebhookHandler) handleGenericWebhookTask(ctx context.Context, provider string, integration genericWebhookIntegration, cfg integrationRuntimeConfig, body []byte) (Task, error) {
	if cfg.ProjectID == "" || cfg.RepositoryID == "" || cfg.CreatedBy == "" {
		return Task{}, errors.New("integration config requires project_id, repository_id, and created_by")
	}

	var payload struct {
		Title       string          `json:"title"`
		Description string          `json:"description"`
		SourceID    string          `json:"source_id"`
		Metadata    json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Task{}, errors.New("invalid webhook payload")
	}
	if strings.TrimSpace(payload.Title) == "" {
		payload.Title = "Webhook task"
	}
	if payload.SourceID == "" {
		payload.SourceID = generateSourceID(provider)
	}
	metadata := payload.Metadata
	if len(metadata) == 0 {
		metadata, _ = json.Marshal(map[string]any{
			"integration_id": integration.ID,
			"provider":       provider,
			"payload":        json.RawMessage(body),
		})
	}
	task, err := h.handler.insertTask(ctx, createTaskOptions{
		ProjectID:    cfg.ProjectID,
		RepositoryID: cfg.RepositoryID,
		CreatedBy:    cfg.CreatedBy,
		Source:       integrationTypeWebhook,
		SourceID:     &payload.SourceID,
		Title:        payload.Title,
		Description:  payload.Description,
		Priority:     cfg.DefaultPriority,
		RiskLevel:    cfg.DefaultRiskLevel,
		TargetBranch: cfg.TargetBranch,
		Metadata:     metadata,
	})
	if err != nil {
		return Task{}, err
	}
	h.touchIntegrationSync(ctx, integration.ID)
	return task, nil
}

func (h *WebhookHandler) touchIntegrationSync(ctx context.Context, integrationID string) {
	if h.handler == nil || integrationID == "" {
		return
	}
	now := time.Now().UTC()
	_, _ = h.handler.db.ExecContext(ctx, `
			UPDATE integrations
			SET last_synced_at = $1, updated_at = $1
			WHERE id = $2 AND deleted_at IS NULL
		`, now, integrationID)
}

func validateGenericWebhook(payload []byte, signature, secret string) bool {
	if strings.HasPrefix(signature, "sha256=") {
		return validateGitHubWebhook(payload, signature, secret)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

func webhookSignatureFromRequest(r *http.Request) string {
	candidates := []string{
		"X-Linear-Signature",
		"X-Slack-Signature",
		"X-Discord-Signature",
		"X-Webhook-Signature",
		"X-Hub-Signature-256",
	}
	for _, key := range candidates {
		if value := r.Header.Get(key); value != "" {
			return value
		}
	}
	return ""
}

func nullableString(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func generateSourceID(provider string) string {
	return provider + sourceIDSeparator + uuid.NewString()
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

// validateLinearWebhook validates the HMAC-SHA256 hex signature of a Linear webhook payload.
func validateLinearWebhook(payload []byte, signature, secret string) bool {
	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)
	return hmac.Equal(sigBytes, expected)
}

// validateSlackWebhook validates the HMAC-SHA256 signature of a Slack webhook payload.
// Slack signatures use the format "v0=<hex>" over the base string "v0:timestamp:body".
func validateSlackWebhook(payload []byte, signature, timestamp, secret string) bool {
	if !strings.HasPrefix(signature, "v0=") {
		return false
	}
	sigHex := strings.TrimPrefix(signature, "v0=")
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	base := []byte("v0:" + timestamp + ":")
	base = append(base, payload...)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(base)
	expected := mac.Sum(nil)
	return hmac.Equal(sigBytes, expected)
}
