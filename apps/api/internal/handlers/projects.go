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

// Project represents a project record.
type Project struct {
	ID          string          `json:"id"`
	OrgID       string          `json:"organization_id"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	Description *string         `json:"description,omitempty"`
	Settings    json.RawMessage `json:"settings,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// CreateProjectRequest is the request body for creating a project.
type CreateProjectRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
}

// ListProjects returns all projects for an organization.
func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, organization_id, name, slug, description, settings, created_at, updated_at
		FROM projects
		WHERE organization_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var desc sql.NullString
		var settings sql.NullString
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Slug, &desc, &settings, &p.CreatedAt, &p.UpdatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if desc.Valid {
			p.Description = &desc.String
		}
		if settings.Valid {
			p.Settings = json.RawMessage(settings.String)
		}
		projects = append(projects, p)
	}

	if projects == nil {
		projects = []Project{}
	}
	respond.JSON(w, http.StatusOK, projects)
}

// CreateProject creates a new project within an organization.
func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	var req CreateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	if req.Name == "" || req.Slug == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("name and slug are required"))
		return
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	var desc interface{}
	if req.Description != "" {
		desc = req.Description
	}

	_, err := h.db.ExecContext(ctx, `
		INSERT INTO projects (id, organization_id, name, slug, description, settings, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, '{}', $6, $7)
	`, id, orgID, req.Name, req.Slug, desc, now, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusCreated, Project{
		ID:        id,
		OrgID:     orgID,
		Name:      req.Name,
		Slug:      req.Slug,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// GetProject returns a single project by ID.
func (h *Handler) GetProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}

	var p Project
	var desc sql.NullString
	var settings sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT id, organization_id, name, slug, description, settings, created_at, updated_at
		FROM projects
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&p.ID, &p.OrgID, &p.Name, &p.Slug, &desc, &settings, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("project not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if desc.Valid {
		p.Description = &desc.String
	}
	if settings.Valid {
		p.Settings = json.RawMessage(settings.String)
	}

	respond.JSON(w, http.StatusOK, p)
}
