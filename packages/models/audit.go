package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ActorType constants for who performed an action.
const (
	ActorTypeUser   = "user"
	ActorTypeAgent  = "agent"
	ActorTypeSystem = "system"
)

// AuditLog represents an immutable record of an action taken in the system.
type AuditLog struct {
	ID           string          `json:"id"`
	OrganizationID string        `json:"organization_id"`
	ActorType    string          `json:"actor_type"`
	ActorID      *string         `json:"actor_id,omitempty"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   *string         `json:"resource_id,omitempty"`
	Details      json.RawMessage `json:"details,omitempty"`
	IPAddress    *string         `json:"ip_address,omitempty"`
	UserAgent    *string         `json:"user_agent,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// Validate checks that the audit log has required fields.
func (a *AuditLog) Validate() error {
	if a.OrganizationID == "" {
		return errors.New("audit log organization_id is required")
	}
	if a.ActorType == "" {
		return errors.New("audit log actor_type is required")
	}
	if a.Action == "" {
		return errors.New("audit log action is required")
	}
	if a.ResourceType == "" {
		return errors.New("audit log resource_type is required")
	}
	return nil
}

// NullAuditLog returns an AuditLog from sql.Null fields.
func NullAuditLog(id sql.NullString, orgID sql.NullString, actorType sql.NullString, actorID sql.NullString, action sql.NullString, resourceType sql.NullString, resourceID sql.NullString, details sql.NullString, ipAddress sql.NullString, userAgent sql.NullString, createdAt sql.NullTime) *AuditLog {
	a := &AuditLog{}
	if id.Valid {
		a.ID = id.String
	}
	if orgID.Valid {
		a.OrganizationID = orgID.String
	}
	if actorType.Valid {
		a.ActorType = actorType.String
	}
	if actorID.Valid {
		aid := actorID.String
		a.ActorID = &aid
	}
	if action.Valid {
		a.Action = action.String
	}
	if resourceType.Valid {
		a.ResourceType = resourceType.String
	}
	if resourceID.Valid {
		rid := resourceID.String
		a.ResourceID = &rid
	}
	if details.Valid {
		a.Details = json.RawMessage(details.String)
	}
	if ipAddress.Valid {
		ip := ipAddress.String
		a.IPAddress = &ip
	}
	if userAgent.Valid {
		ua := userAgent.String
		a.UserAgent = &ua
	}
	if createdAt.Valid {
		a.CreatedAt = createdAt.Time
	}
	return a
}
