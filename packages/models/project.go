package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Project represents a software project within an organization.
type Project struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"organization_id"`
	Name           string          `json:"name"`
	Slug           string          `json:"slug"`
	Description    *string         `json:"description,omitempty"`
	Settings       json.RawMessage `json:"settings,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	DeletedAt      *time.Time      `json:"deleted_at,omitempty"`
}

// Validate checks that the project has required fields.
func (p *Project) Validate() error {
	if p.Name == "" {
		return errors.New("project name is required")
	}
	if p.Slug == "" {
		return errors.New("project slug is required")
	}
	if p.OrganizationID == "" {
		return errors.New("project organization_id is required")
	}
	return nil
}

// NullProject returns a Project from sql.Null fields.
func NullProject(id sql.NullString, orgID sql.NullString, name sql.NullString, slug sql.NullString, description sql.NullString, settings sql.NullString, createdAt sql.NullTime, updatedAt sql.NullTime, deletedAt sql.NullTime) *Project {
	p := &Project{}
	if id.Valid {
		p.ID = id.String
	}
	if orgID.Valid {
		p.OrganizationID = orgID.String
	}
	if name.Valid {
		p.Name = name.String
	}
	if slug.Valid {
		p.Slug = slug.String
	}
	if description.Valid {
		d := description.String
		p.Description = &d
	}
	if settings.Valid {
		p.Settings = json.RawMessage(settings.String)
	}
	if createdAt.Valid {
		p.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		p.UpdatedAt = updatedAt.Time
	}
	if deletedAt.Valid {
		p.DeletedAt = &deletedAt.Time
	}
	return p
}
