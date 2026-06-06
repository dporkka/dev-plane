package models

import (
	"database/sql"
	"errors"
	"time"
)

// SecretProvider constants for supported secret backends.
const (
	SecretProviderSOPS        = "sops"
	SecretProviderEnv         = "env"
	SecretProviderVault       = "vault"
	SecretProviderEncryptedDB = "encrypted_db"
)

// SecretScope constants for secret scoping.
const (
	SecretScopeDev     = "dev"
	SecretScopeStaging = "staging"
	SecretScopeProd    = "prod"
)

// SecretReference stores a reference to a secret without holding the actual value.
// The secret value is resolved at runtime by the configured provider.
type SecretReference struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	ProjectID      *string    `json:"project_id,omitempty"`
	Name           string     `json:"name"`
	Scope          string     `json:"scope"`    // dev, staging, prod
	Provider       string     `json:"provider"` // sops, env, vault, encrypted_db
	KeyPath        string     `json:"key_path"` // path to actual secret
	Description    string     `json:"description,omitempty"`
	LastRotatedAt  *time.Time `json:"last_rotated_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Validate checks that the secret reference has required fields.
func (s *SecretReference) Validate() error {
	if s.OrganizationID == "" {
		return errors.New("secret_reference organization_id is required")
	}
	if s.Name == "" {
		return errors.New("secret_reference name is required")
	}
	if s.Scope == "" {
		return errors.New("secret_reference scope is required")
	}
	if s.Provider == "" {
		return errors.New("secret_reference provider is required")
	}
	if s.KeyPath == "" {
		return errors.New("secret_reference key_path is required")
	}
	return nil
}

// ShouldRotate returns true if the secret has never been rotated or was
// rotated more than the given duration ago.
func (s *SecretReference) ShouldRotate(period time.Duration) bool {
	if s.LastRotatedAt == nil {
		return true
	}
	return time.Since(*s.LastRotatedAt) > period
}

// NullSecretReference returns a SecretReference from sql.Null fields.
func NullSecretReference(
	id sql.NullString,
	orgID sql.NullString,
	projectID sql.NullString,
	name sql.NullString,
	scope sql.NullString,
	provider sql.NullString,
	keyPath sql.NullString,
	description sql.NullString,
	lastRotatedAt sql.NullTime,
	createdAt sql.NullTime,
	updatedAt sql.NullTime,
) *SecretReference {
	s := &SecretReference{}
	if id.Valid {
		s.ID = id.String
	}
	if orgID.Valid {
		s.OrganizationID = orgID.String
	}
	if projectID.Valid {
		p := projectID.String
		s.ProjectID = &p
	}
	if name.Valid {
		s.Name = name.String
	}
	if scope.Valid {
		s.Scope = scope.String
	}
	if provider.Valid {
		s.Provider = provider.String
	}
	if keyPath.Valid {
		s.KeyPath = keyPath.String
	}
	if description.Valid {
		s.Description = description.String
	}
	if lastRotatedAt.Valid {
		s.LastRotatedAt = &lastRotatedAt.Time
	}
	if createdAt.Valid {
		s.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		s.UpdatedAt = updatedAt.Time
	}
	return s
}
