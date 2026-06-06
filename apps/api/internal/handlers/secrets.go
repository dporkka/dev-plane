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

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/respond"
	secretstore "github.com/ai-dev-control-plane/api/internal/secrets"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

type SecretResponse struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	ProjectID      *string    `json:"project_id,omitempty"`
	Name           string     `json:"name"`
	Scope          string     `json:"scope"`
	Provider       string     `json:"provider"`
	Description    string     `json:"description,omitempty"`
	LastRotatedAt  *time.Time `json:"last_rotated_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type CreateSecretRequest struct {
	ProjectID   *string `json:"project_id,omitempty"`
	Name        string  `json:"name"`
	Scope       string  `json:"scope"`
	Description string  `json:"description,omitempty"`
	Value       string  `json:"value"`
}

type RotateSecretRequest struct {
	Value string `json:"value"`
}

func (h *Handler) ListSecrets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, organization_id, project_id, name, scope, provider, description,
		       last_rotated_at, created_at, updated_at
		FROM secret_references
		WHERE organization_id = $1 AND deleted_at IS NULL
		ORDER BY scope, name
	`, orgID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var secrets []SecretResponse
	for rows.Next() {
		secret, err := scanSecretResponse(rows)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		secrets = append(secrets, secret)
	}
	if secrets == nil {
		secrets = []SecretResponse{}
	}
	respond.JSON(w, http.StatusOK, secrets)
}

func (h *Handler) CreateSecret(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}
	if h.secretManager == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("encrypted secret storage is not configured"))
		return
	}

	var req CreateSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" || req.Value == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("name and value are required"))
		return
	}
	if req.Scope == "" {
		req.Scope = models.SecretScopeDev
	}
	if !h.authorizeSecretOperation(w, r, orgID, capability.OpWriteSecret, req.Name, req.Scope) {
		return
	}

	actorType, actorID := requestActor(r)
	stored, err := h.secretManager.Store(ctx, secretstore.StoreRequest{
		OrganizationID: orgID,
		ProjectID:      req.ProjectID,
		Name:           req.Name,
		Scope:          req.Scope,
		Description:    req.Description,
		Value:          []byte(req.Value),
		ActorType:      actorType,
		ActorID:        actorID,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	respond.JSON(w, http.StatusCreated, secretReferenceResponse(stored.Reference))
}

func (h *Handler) RotateSecret(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	secretID := chi.URLParam(r, "id")
	if secretID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("secret id is required"))
		return
	}
	if h.secretManager == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("encrypted secret storage is not configured"))
		return
	}

	ref, err := h.loadSecretReference(ctx, secretID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("secret not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	var req RotateSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.Value == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("value is required"))
		return
	}
	if !h.authorizeSecretOperation(w, r, ref.OrganizationID, capability.OpRotateSecret, ref.Name, ref.Scope) {
		return
	}

	actorType, actorID := requestActor(r)
	stored, err := h.secretManager.Rotate(ctx, secretstore.RotationRequest{
		SecretID:  secretID,
		Value:     []byte(req.Value),
		ActorType: actorType,
		ActorID:   actorID,
	})
	if errors.Is(err, secretstore.ErrSecretNotFound) {
		respond.Error(w, http.StatusNotFound, errors.New("secret not found"))
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	respond.JSON(w, http.StatusOK, secretReferenceResponse(stored.Reference))
}

type secretScanner interface {
	Scan(dest ...any) error
}

func scanSecretResponse(row secretScanner) (SecretResponse, error) {
	var secret SecretResponse
	var projectID, description sql.NullString
	var lastRotated sql.NullTime
	err := row.Scan(
		&secret.ID, &secret.OrganizationID, &projectID, &secret.Name, &secret.Scope,
		&secret.Provider, &description, &lastRotated, &secret.CreatedAt, &secret.UpdatedAt,
	)
	if err != nil {
		return secret, err
	}
	if projectID.Valid {
		secret.ProjectID = &projectID.String
	}
	if description.Valid {
		secret.Description = description.String
	}
	if lastRotated.Valid {
		secret.LastRotatedAt = &lastRotated.Time
	}
	return secret, nil
}

func secretReferenceResponse(ref models.SecretReference) SecretResponse {
	return SecretResponse{
		ID:             ref.ID,
		OrganizationID: ref.OrganizationID,
		ProjectID:      ref.ProjectID,
		Name:           ref.Name,
		Scope:          ref.Scope,
		Provider:       ref.Provider,
		Description:    ref.Description,
		LastRotatedAt:  ref.LastRotatedAt,
		CreatedAt:      ref.CreatedAt,
		UpdatedAt:      ref.UpdatedAt,
	}
}

func (h *Handler) loadSecretReference(ctx context.Context, secretID string) (*models.SecretReference, error) {
	var ref models.SecretReference
	var projectID, description sql.NullString
	var lastRotated sql.NullTime
	err := h.db.QueryRowContext(ctx, `
		SELECT id, organization_id, project_id, name, scope, provider, key_path,
		       description, last_rotated_at, created_at, updated_at
		FROM secret_references
		WHERE id = $1 AND deleted_at IS NULL
	`, secretID).Scan(
		&ref.ID, &ref.OrganizationID, &projectID, &ref.Name, &ref.Scope, &ref.Provider,
		&ref.KeyPath, &description, &lastRotated, &ref.CreatedAt, &ref.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if projectID.Valid {
		ref.ProjectID = &projectID.String
	}
	if description.Valid {
		ref.Description = description.String
	}
	if lastRotated.Valid {
		ref.LastRotatedAt = &lastRotated.Time
	}
	return &ref, nil
}

func (h *Handler) authorizeSecretOperation(w http.ResponseWriter, r *http.Request, orgID, operation, resource, scope string) bool {
	userClaims := auth.UserFromContext(r.Context())
	var user *models.User
	if userClaims != nil {
		user = &models.User{
			ID:             userClaims.UserID,
			OrganizationID: userClaims.OrgID,
			Email:          userClaims.Email,
			Role:           userClaims.Role,
		}
	}
	result, err := h.kernel().Evaluate(r.Context(), capability.Request{
		ActorType:    "human",
		User:         user,
		Organization: &models.Organization{ID: orgID},
		Operation:    operation,
		Resource:     resource,
		Details: map[string]any{
			"organization_id": orgID,
			"scope":           scope,
		},
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("authorize secret operation: %w", err))
		return false
	}
	if result.Effect == policies.EffectDeny {
		respond.Error(w, http.StatusForbidden, errors.New(result.Reason))
		return false
	}
	if result.RequiredApproval {
		respond.Error(w, http.StatusLocked, errors.New(result.Reason))
		return false
	}
	return true
}

func requestActor(r *http.Request) (string, string) {
	userClaims := auth.UserFromContext(r.Context())
	if userClaims == nil {
		return "anonymous", ""
	}
	return "human", userClaims.UserID
}
