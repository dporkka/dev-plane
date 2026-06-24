package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/gateway"
)

// Integration represents an integration record.
type Integration struct {
	ID                 string          `json:"id"`
	OrgID              string          `json:"organization_id"`
	IntegrationType    string          `json:"integration_type"`
	DisplayName        string          `json:"display_name"`
	Config             json.RawMessage `json:"config,omitempty"`
	CredentialsEncrypted *string       `json:"credentials_encrypted,omitempty"`
	Status             string          `json:"status"`
	WebhookURL         *string         `json:"webhook_url,omitempty"`
	LastSyncedAt       *time.Time      `json:"last_synced_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// CreateIntegrationRequest is the request body for creating an integration.
type CreateIntegrationRequest struct {
	IntegrationType string          `json:"integration_type"`
	DisplayName     string          `json:"display_name"`
	Config          json.RawMessage `json:"config,omitempty"`
	Token           *string         `json:"token,omitempty"`
	WebhookURL      *string         `json:"webhook_url,omitempty"`
}

// UpdateIntegrationRequest is the request body for updating an integration.
type UpdateIntegrationRequest struct {
	DisplayName *string         `json:"display_name,omitempty"`
	Config      json.RawMessage `json:"config,omitempty"`
	Status      *string         `json:"status,omitempty"`
	Token       *string         `json:"token,omitempty"`
	WebhookURL  *string         `json:"webhook_url,omitempty"`
}

// ListIntegrations returns all integrations for an organization.
func (h *Handler) ListIntegrations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	if err := authz.AuthorizeOrganization(ctx, h.db, user, orgID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, organization_id, integration_type, display_name, config,
		       credentials_encrypted, status, webhook_url, last_synced_at, created_at, updated_at
		FROM integrations
		WHERE organization_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var integrations []Integration
	for rows.Next() {
		var i Integration
		var config sql.NullString
		var credentials sql.NullString
		var webhookURL sql.NullString
		var lastSync sql.NullTime
		if err := rows.Scan(&i.ID, &i.OrgID, &i.IntegrationType, &i.DisplayName, &config,
			&credentials, &i.Status, &webhookURL, &lastSync, &i.CreatedAt, &i.UpdatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if config.Valid {
			i.Config = json.RawMessage(config.String)
		}
		if credentials.Valid {
			s := credentials.String
			i.CredentialsEncrypted = &s
		}
		if webhookURL.Valid {
			i.WebhookURL = &webhookURL.String
		}
		if lastSync.Valid {
			i.LastSyncedAt = &lastSync.Time
		}
		integrations = append(integrations, i)
	}

	if integrations == nil {
		integrations = []Integration{}
	}
	respond.JSON(w, http.StatusOK, integrations)
}

// CreateIntegration creates a new integration.
func (h *Handler) CreateIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	if err := authz.AuthorizeOrganization(ctx, h.db, user, orgID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
		return
	}

	var req CreateIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	if req.IntegrationType == "" || req.DisplayName == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("integration_type and display_name are required"))
		return
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	var config interface{}
	if len(req.Config) > 0 {
		config = req.Config
	} else {
		config = "{}"
	}

	var credentials interface{}
	if req.Token != nil && *req.Token != "" {
		credentials = *req.Token
	}

	var webhookURL interface{}
	if req.WebhookURL != nil && *req.WebhookURL != "" {
		webhookURL = *req.WebhookURL
	}

	status := "pending"
	if req.Token != nil || req.WebhookURL != nil {
		if err := h.validateIntegration(ctx, req.IntegrationType, req.Token, req.WebhookURL); err != nil {
			respond.Error(w, http.StatusBadRequest, fmt.Errorf("integration validation failed: %w", err))
			return
		}
		status = "connected"
	}

	_, err := h.db.ExecContext(ctx, `
		INSERT INTO integrations (id, organization_id, integration_type, display_name, config, credentials_encrypted, webhook_url, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
	`, id, orgID, req.IntegrationType, req.DisplayName, config, credentials, webhookURL, status, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusCreated, Integration{
		ID:              id,
		OrgID:           orgID,
		IntegrationType: req.IntegrationType,
		DisplayName:     req.DisplayName,
		Config:          req.Config,
		Status:          status,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
}

// UpdateIntegration updates an existing integration.
func (h *Handler) UpdateIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("integration id is required"))
		return
	}

	if err := authz.AuthorizeIntegration(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("integration not found"))
		return
	}

	var req UpdateIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	now := time.Now().UTC()

	var displayName interface{}
	if req.DisplayName != nil {
		displayName = *req.DisplayName
	}
	var status interface{}
	if req.Status != nil {
		status = *req.Status
	}
	var config interface{}
	if len(req.Config) > 0 {
		config = req.Config
	}
	var credentials interface{}
	if req.Token != nil && *req.Token != "" {
		credentials = *req.Token
	}
	var webhookURL interface{}
	if req.WebhookURL != nil && *req.WebhookURL != "" {
		webhookURL = *req.WebhookURL
	}

	if req.Token != nil || req.WebhookURL != nil {
		var integrationType string
		if err := h.db.QueryRowContext(ctx, `
			SELECT integration_type FROM integrations WHERE id = $1 AND deleted_at IS NULL
		`, id).Scan(&integrationType); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				respond.Error(w, http.StatusNotFound, errors.New("integration not found"))
				return
			}
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if err := h.validateIntegration(ctx, integrationType, req.Token, req.WebhookURL); err != nil {
			respond.Error(w, http.StatusBadRequest, fmt.Errorf("integration validation failed: %w", err))
			return
		}
		status = "connected"
	}

	result, err := h.db.ExecContext(ctx, `
		UPDATE integrations SET
			display_name = COALESCE($1, display_name),
			config = COALESCE($2, config),
			status = COALESCE($3, status),
			credentials_encrypted = COALESCE($4, credentials_encrypted),
			webhook_url = COALESCE($5, webhook_url),
			updated_at = $6
		WHERE id = $7 AND deleted_at IS NULL
	`, displayName, config, status, credentials, webhookURL, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		respond.Error(w, http.StatusNotFound, errors.New("integration not found"))
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) validateIntegration(ctx context.Context, integrationType string, token, webhookURL *string) error {
	switch integrationType {
	case "linear":
		if token == nil || *token == "" {
			return errors.New("linear integration requires a token")
		}
		return gateway.NewLinearGateway(*token).Validate(ctx)
	case "slack":
		if token == nil || *token == "" {
			return errors.New("slack integration requires a token")
		}
		return gateway.NewSlackGateway(*token).Validate(ctx)
	case "discord":
		botToken := ""
		if token != nil {
			botToken = *token
		}
		webhook := ""
		if webhookURL != nil {
			webhook = *webhookURL
		}
		if botToken == "" && webhook == "" {
			return errors.New("discord integration requires a token or webhook_url")
		}
		return gateway.NewDiscordGateway(botToken, webhook).Validate(ctx)
	case "github":
		// GitHub integrations are managed through the auth flow, not token entry here.
		return nil
	default:
		return nil
	}
}

// DeleteIntegration soft-deletes an integration.
func (h *Handler) DeleteIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("integration id is required"))
		return
	}

	if err := authz.AuthorizeIntegration(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("integration not found"))
		return
	}

	now := time.Now().UTC()
	result, err := h.db.ExecContext(ctx, `
		UPDATE integrations SET deleted_at = $1, status = 'disconnected', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		respond.Error(w, http.StatusNotFound, errors.New("integration not found"))
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
