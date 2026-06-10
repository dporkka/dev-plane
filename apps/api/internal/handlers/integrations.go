package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/respond"
)

// Integration represents an integration record.
type Integration struct {
	ID                   string               `json:"id"`
	OrgID                string               `json:"organization_id"`
	IntegrationType      string               `json:"integration_type"`
	DisplayName          string               `json:"display_name"`
	Config               json.RawMessage      `json:"config,omitempty"`
	CredentialsEncrypted *string              `json:"credentials_encrypted,omitempty"`
	Status               string               `json:"status"`
	WebhookURL           *string              `json:"webhook_url,omitempty"`
	LastSyncedAt         *time.Time           `json:"last_synced_at,omitempty"`
	Provider             *IntegrationProvider `json:"provider,omitempty"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

// CreateIntegrationRequest is the request body for creating an integration.
type CreateIntegrationRequest struct {
	IntegrationType string          `json:"integration_type"`
	DisplayName     string          `json:"display_name"`
	Config          json.RawMessage `json:"config,omitempty"`
}

// UpdateIntegrationRequest is the request body for updating an integration.
type UpdateIntegrationRequest struct {
	DisplayName *string         `json:"display_name,omitempty"`
	Config      json.RawMessage `json:"config,omitempty"`
	Status      *string         `json:"status,omitempty"`
}

// ListIntegrationProviders returns the supported integration catalog.
func (h *Handler) ListIntegrationProviders(w http.ResponseWriter, _ *http.Request) {
	respond.JSON(w, http.StatusOK, SupportedIntegrationProviders())
}

// ListIntegrations returns all integrations for an organization.
func (h *Handler) ListIntegrations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, organization_id, integration_type, display_name, config,
		       status, webhook_url, last_synced_at, created_at, updated_at
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
		var webhookURL sql.NullString
		var lastSync sql.NullTime
		if err := rows.Scan(&i.ID, &i.OrgID, &i.IntegrationType, &i.DisplayName, &config,
			&i.Status, &webhookURL, &lastSync, &i.CreatedAt, &i.UpdatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if config.Valid {
			i.Config = json.RawMessage(config.String)
		}
		if webhookURL.Valid {
			i.WebhookURL = &webhookURL.String
		}
		if lastSync.Valid {
			i.LastSyncedAt = &lastSync.Time
		}
		if provider, ok := integrationProviderByType(i.IntegrationType); ok {
			providerCopy := provider
			i.Provider = &providerCopy
		}
		integrations = append(integrations, i)
	}

	if integrations == nil {
		integrations = []Integration{}
	}
	respond.JSON(w, http.StatusOK, integrations)
}

// GetIntegration returns a single integration by ID.
func (h *Handler) GetIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("integration id is required"))
		return
	}

	var i Integration
	var config sql.NullString
	var webhookURL sql.NullString
	var lastSync sql.NullTime
	err := h.db.QueryRowContext(ctx, `
		SELECT id, organization_id, integration_type, display_name, config,
		       status, webhook_url, last_synced_at, created_at, updated_at
		FROM integrations
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&i.ID, &i.OrgID, &i.IntegrationType, &i.DisplayName, &config, &i.Status, &webhookURL, &lastSync, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("integration not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if config.Valid {
		i.Config = json.RawMessage(config.String)
	}
	if webhookURL.Valid {
		i.WebhookURL = &webhookURL.String
	}
	if lastSync.Valid {
		i.LastSyncedAt = &lastSync.Time
	}
	if provider, ok := integrationProviderByType(i.IntegrationType); ok {
		providerCopy := provider
		i.Provider = &providerCopy
	}

	respond.JSON(w, http.StatusOK, i)
}

// CreateIntegration creates a new integration.
func (h *Handler) CreateIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
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
	if !supportedIntegrationType(req.IntegrationType) {
		respond.Error(w, http.StatusBadRequest, errors.New("unsupported integration_type"))
		return
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	var config interface{}
	if len(req.Config) > 0 {
		config = req.Config
	} else {
		req.Config = json.RawMessage(`{}`)
		config = req.Config
	}

	webhookURL := integrationWebhookPath(req.IntegrationType, id)
	var webhookURLValue interface{}
	if webhookURL != "" {
		webhookURLValue = webhookURL
	}

	_, err := h.db.ExecContext(ctx, `
		INSERT INTO integrations (id, organization_id, integration_type, display_name, config, status, webhook_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7, $7)
	`, id, orgID, req.IntegrationType, req.DisplayName, config, webhookURLValue, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	integration := Integration{
		ID:              id,
		OrgID:           orgID,
		IntegrationType: req.IntegrationType,
		DisplayName:     req.DisplayName,
		Config:          req.Config,
		Status:          "pending",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if webhookURL != "" {
		integration.WebhookURL = &webhookURL
	}
	if provider, ok := integrationProviderByType(req.IntegrationType); ok {
		providerCopy := provider
		integration.Provider = &providerCopy
	}
	respond.JSON(w, http.StatusCreated, integration)
}

// UpdateIntegration updates an existing integration.
func (h *Handler) UpdateIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("integration id is required"))
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

	result, err := h.db.ExecContext(ctx, `
		UPDATE integrations SET
			display_name = COALESCE($1, display_name),
			config = COALESCE($2, config),
			status = COALESCE($3, status),
			updated_at = $4
		WHERE id = $5 AND deleted_at IS NULL
	`, displayName, config, status, now, id)
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

// DeleteIntegration soft-deletes an integration.
func (h *Handler) DeleteIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("integration id is required"))
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
