package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Role constants for users within an organization.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// User represents a person with access to the system.
type User struct {
	ID              string          `json:"id"`
	OrganizationID  string          `json:"organization_id"`
	Email           string          `json:"email"`
	Name            *string         `json:"name,omitempty"`
	AvatarURL       *string         `json:"avatar_url,omitempty"`
	Role            string          `json:"role"`
	GitHubID        *string         `json:"github_id,omitempty"`
	GitHubUsername  *string         `json:"github_username,omitempty"`
	Settings        json.RawMessage `json:"settings,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	DeletedAt       *time.Time      `json:"deleted_at,omitempty"`
}

// Validate checks that the user has required fields.
func (u *User) Validate() error {
	if u.Email == "" {
		return errors.New("user email is required")
	}
	if u.OrganizationID == "" {
		return errors.New("user organization_id is required")
	}
	if u.Role != RoleOwner && u.Role != RoleAdmin && u.Role != RoleMember {
		return errors.New("invalid user role")
	}
	return nil
}

// NullUser returns a User from sql.Null fields.
func NullUser(id sql.NullString, orgID sql.NullString, email sql.NullString, name sql.NullString, avatarURL sql.NullString, role sql.NullString, githubID sql.NullString, githubUsername sql.NullString, settings sql.NullString, createdAt sql.NullTime, updatedAt sql.NullTime, deletedAt sql.NullTime) *User {
	u := &User{}
	if id.Valid {
		u.ID = id.String
	}
	if orgID.Valid {
		u.OrganizationID = orgID.String
	}
	if email.Valid {
		u.Email = email.String
	}
	if name.Valid {
		n := name.String
		u.Name = &n
	}
	if avatarURL.Valid {
		a := avatarURL.String
		u.AvatarURL = &a
	}
	if role.Valid {
		u.Role = role.String
	}
	if githubID.Valid {
		g := githubID.String
		u.GitHubID = &g
	}
	if githubUsername.Valid {
		gu := githubUsername.String
		u.GitHubUsername = &gu
	}
	if settings.Valid {
		u.Settings = json.RawMessage(settings.String)
	}
	if createdAt.Valid {
		u.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		u.UpdatedAt = updatedAt.Time
	}
	if deletedAt.Valid {
		u.DeletedAt = &deletedAt.Time
	}
	return u
}
