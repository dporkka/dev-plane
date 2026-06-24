package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// Policy represents a policy record.
type Policy struct {
	ID           string          `json:"id"`
	OrgID        string          `json:"organization_id"`
	ProjectID    *string         `json:"project_id,omitempty"`
	Name         string          `json:"name"`
	ResourceType string          `json:"resource_type"`
	Action       string          `json:"action"`
	Effect       string          `json:"effect"`
	Conditions   json.RawMessage `json:"conditions,omitempty"`
	Priority     int             `json:"priority"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// CreatePolicyRequest is the request body for creating a policy.
type CreatePolicyRequest struct {
	Name         string          `json:"name"`
	ResourceType string          `json:"resource_type"`
	Action       string          `json:"action"`
	Effect       string          `json:"effect"`
	Conditions   json.RawMessage `json:"conditions,omitempty"`
	Priority     *int            `json:"priority,omitempty"`
}

// ListPolicies returns all policies for an organization.
func (h *Handler) ListPolicies(w http.ResponseWriter, r *http.Request) {
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
		SELECT id, organization_id, project_id, name, resource_type, action, effect,
		       conditions, priority, created_at, updated_at
		FROM policies
		WHERE organization_id = $1
		ORDER BY priority ASC, created_at DESC
	`, orgID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var policies []Policy
	for rows.Next() {
		var p Policy
		var projectID sql.NullString
		var conditions sql.NullString
		if err := rows.Scan(&p.ID, &p.OrgID, &projectID, &p.Name, &p.ResourceType, &p.Action,
			&p.Effect, &conditions, &p.Priority, &p.CreatedAt, &p.UpdatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if projectID.Valid {
			p.ProjectID = &projectID.String
		}
		if conditions.Valid {
			p.Conditions = json.RawMessage(conditions.String)
		}
		policies = append(policies, p)
	}

	if policies == nil {
		policies = []Policy{}
	}
	respond.JSON(w, http.StatusOK, policies)
}

// CreatePolicy creates a new policy.
func (h *Handler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
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

	var req CreatePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	if req.Name == "" || req.ResourceType == "" || req.Action == "" || req.Effect == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("name, resource_type, action, and effect are required"))
		return
	}

	priority := 100
	if req.Priority != nil {
		priority = *req.Priority
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	var conditions interface{}
	if len(req.Conditions) > 0 {
		conditions = req.Conditions
	} else {
		conditions = "{}"
	}

	_, err := h.db.ExecContext(ctx, `
		INSERT INTO policies (id, organization_id, name, resource_type, action, effect, conditions, priority, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
	`, id, orgID, req.Name, req.ResourceType, req.Action, req.Effect, conditions, priority, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusCreated, Policy{
		ID:           id,
		OrgID:        orgID,
		Name:         req.Name,
		ResourceType: req.ResourceType,
		Action:       req.Action,
		Effect:       req.Effect,
		Priority:     priority,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
}
