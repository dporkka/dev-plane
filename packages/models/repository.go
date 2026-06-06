package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ConnectionStatus constants for repository connections.
const (
	ConnectionStatusPending   = "pending"
	ConnectionStatusConnected = "connected"
	ConnectionStatusError     = "error"
)

// Repository represents a connected GitHub repository.
type Repository struct {
	ID               string          `json:"id"`
	ProjectID        string          `json:"project_id"`
	GitHubID         *int64          `json:"github_id,omitempty"`
	Owner            string          `json:"owner"`
	Name             string          `json:"name"`
	FullName         string          `json:"full_name"`
	CloneURL         string          `json:"clone_url"`
	DefaultBranch    string          `json:"default_branch"`
	Private          bool            `json:"private"`
	ConnectionStatus string          `json:"connection_status"`
	LastSyncedAt     *time.Time      `json:"last_synced_at,omitempty"`
	WebhookSecret    *string         `json:"webhook_secret,omitempty"`
	Settings         json.RawMessage `json:"settings,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	DeletedAt        *time.Time      `json:"deleted_at,omitempty"`
}

// Validate checks that the repository has required fields.
func (r *Repository) Validate() error {
	if r.Owner == "" {
		return errors.New("repository owner is required")
	}
	if r.Name == "" {
		return errors.New("repository name is required")
	}
	if r.FullName == "" {
		return errors.New("repository full_name is required")
	}
	if r.CloneURL == "" {
		return errors.New("repository clone_url is required")
	}
	if r.ProjectID == "" {
		return errors.New("repository project_id is required")
	}
	return nil
}

// NullRepository returns a Repository from sql.Null fields.
func NullRepository(id sql.NullString, projectID sql.NullString, githubID sql.NullInt64, owner sql.NullString, name sql.NullString, fullName sql.NullString, cloneURL sql.NullString, defaultBranch sql.NullString, private sql.NullBool, connStatus sql.NullString, lastSyncedAt sql.NullTime, webhookSecret sql.NullString, settings sql.NullString, createdAt sql.NullTime, updatedAt sql.NullTime, deletedAt sql.NullTime) *Repository {
	r := &Repository{}
	if id.Valid {
		r.ID = id.String
	}
	if projectID.Valid {
		r.ProjectID = projectID.String
	}
	if githubID.Valid {
		gid := githubID.Int64
		r.GitHubID = &gid
	}
	if owner.Valid {
		r.Owner = owner.String
	}
	if name.Valid {
		r.Name = name.String
	}
	if fullName.Valid {
		r.FullName = fullName.String
	}
	if cloneURL.Valid {
		r.CloneURL = cloneURL.String
	}
	if defaultBranch.Valid {
		r.DefaultBranch = defaultBranch.String
	}
	if private.Valid {
		r.Private = private.Bool
	}
	if connStatus.Valid {
		r.ConnectionStatus = connStatus.String
	}
	if lastSyncedAt.Valid {
		r.LastSyncedAt = &lastSyncedAt.Time
	}
	if webhookSecret.Valid {
		ws := webhookSecret.String
		r.WebhookSecret = &ws
	}
	if settings.Valid {
		r.Settings = json.RawMessage(settings.String)
	}
	if createdAt.Valid {
		r.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		r.UpdatedAt = updatedAt.Time
	}
	if deletedAt.Valid {
		r.DeletedAt = &deletedAt.Time
	}
	return r
}
