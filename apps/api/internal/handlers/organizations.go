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

// Organization represents an organization record.
type Organization struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Slug      string          `json:"slug"`
	Plan      string          `json:"plan"`
	Settings  json.RawMessage `json:"settings,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// CreateOrganizationRequest is the request body for creating an organization.
type CreateOrganizationRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
	Plan string `json:"plan,omitempty"`
}

// ListOrganizations returns all organizations for the authenticated user.
func (h *Handler) ListOrganizations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT o.id, o.name, o.slug, o.plan, o.settings, o.created_at, o.updated_at
		FROM organizations o
		JOIN users u ON u.organization_id = o.id
		WHERE u.id = $1 AND o.deleted_at IS NULL
	`, user.UserID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var orgs []Organization
	for rows.Next() {
		var org Organization
		var settings sql.NullString
		if err := rows.Scan(&org.ID, &org.Name, &org.Slug, &org.Plan, &settings, &org.CreatedAt, &org.UpdatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if settings.Valid {
			org.Settings = json.RawMessage(settings.String)
		}
		orgs = append(orgs, org)
	}

	if orgs == nil {
		orgs = []Organization{}
	}
	respond.JSON(w, http.StatusOK, orgs)
}

// CreateOrganization creates a new organization.
func (h *Handler) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	var req CreateOrganizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	if req.Name == "" || req.Slug == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("name and slug are required"))
		return
	}

	plan := req.Plan
	if plan == "" {
		plan = "free"
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	_, err := h.db.ExecContext(ctx, `
		INSERT INTO organizations (id, name, slug, plan, settings, created_at, updated_at)
		VALUES ($1, $2, $3, $4, '{}', $5, $6)
	`, id, req.Name, req.Slug, plan, now, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusCreated, Organization{
		ID:        id,
		Name:      req.Name,
		Slug:      req.Slug,
		Plan:      plan,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// GetOrganization returns a single organization by ID.
func (h *Handler) GetOrganization(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	if err := authz.AuthorizeOrganization(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
		return
	}

	var org Organization
	var settings sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT id, name, slug, plan, settings, created_at, updated_at
		FROM organizations
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&org.ID, &org.Name, &org.Slug, &org.Plan, &settings, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if settings.Valid {
		org.Settings = json.RawMessage(settings.String)
	}

	respond.JSON(w, http.StatusOK, org)
}
