package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Plan tier constants for organizations.
const (
	PlanFree       = "free"
	PlanPro        = "pro"
	PlanEnterprise = "enterprise"
)

// Organization represents a tenant in the system.
type Organization struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Slug      string          `json:"slug"`
	Plan      string          `json:"plan"`
	Settings  json.RawMessage `json:"settings,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	DeletedAt *time.Time      `json:"deleted_at,omitempty"`
}

// Validate checks that the organization has required fields.
func (o *Organization) Validate() error {
	if o.Name == "" {
		return errors.New("organization name is required")
	}
	if o.Slug == "" {
		return errors.New("organization slug is required")
	}
	return nil
}

// NullOrganization returns an Organization from sql.Null fields.
func NullOrganization(id sql.NullString, name sql.NullString, slug sql.NullString, plan sql.NullString, settings sql.NullString, createdAt sql.NullTime, updatedAt sql.NullTime, deletedAt sql.NullTime) *Organization {
	o := &Organization{}
	if id.Valid {
		o.ID = id.String
	}
	if name.Valid {
		o.Name = name.String
	}
	if slug.Valid {
		o.Slug = slug.String
	}
	if plan.Valid {
		o.Plan = plan.String
	}
	if settings.Valid {
		o.Settings = json.RawMessage(settings.String)
	}
	if createdAt.Valid {
		o.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		o.UpdatedAt = updatedAt.Time
	}
	if deletedAt.Valid {
		o.DeletedAt = &deletedAt.Time
	}
	return o
}
