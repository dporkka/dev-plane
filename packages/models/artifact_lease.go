package models

import "time"

// ArtifactLease is the durable lease slot for one artifact path. The raw lease
// capability token is never persisted; only its SHA-256 hash is stored.
type ArtifactLease struct {
	ScopeID      string    `json:"scope_id"`
	ArtifactPath string    `json:"artifact_path"`
	OwnerID      string    `json:"owner_id"`
	TokenHash    string    `json:"-"`
	Generation   int64     `json:"generation"`
	ExpiresAt    time.Time `json:"expires_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (l ArtifactLease) Active(now time.Time) bool {
	return l.OwnerID != "" && l.TokenHash != "" && l.ExpiresAt.After(now)
}
