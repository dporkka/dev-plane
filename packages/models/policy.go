package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Effect constants for policy decisions.
const (
	EffectAllow     = "allow"
	EffectAsk       = "ask"
	EffectDeny      = "deny"
	EffectAdminOnly = "admin_only"
)

// ResourceType constants for what a policy applies to.
const (
	ResourceTypeFile    = "file"
	ResourceTypeCommand = "command"
	ResourceTypeSecret  = "secret"
	ResourceTypeDeploy  = "deploy"
	ResourceTypeGit     = "git"
	ResourceTypeNetwork = "network"
)

// Action constants for what can be done to a resource.
const (
	ActionRead    = "read"
	ActionWrite   = "write"
	ActionExecute = "execute"
	ActionDelete  = "delete"
)

// Policy represents a security or governance rule.
type Policy struct {
	ID           string          `json:"id"`
	OrganizationID string        `json:"organization_id"`
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

// Validate checks that the policy has required fields.
func (p *Policy) Validate() error {
	if p.Name == "" {
		return errors.New("policy name is required")
	}
	if p.OrganizationID == "" {
		return errors.New("policy organization_id is required")
	}
	if p.ResourceType == "" {
		return errors.New("policy resource_type is required")
	}
	if p.Action == "" {
		return errors.New("policy action is required")
	}
	if p.Effect != EffectAllow && p.Effect != EffectAsk && p.Effect != EffectDeny && p.Effect != EffectAdminOnly {
		return errors.New("invalid policy effect")
	}
	return nil
}

// NullPolicy returns a Policy from sql.Null fields.
func NullPolicy(id sql.NullString, orgID sql.NullString, projectID sql.NullString, name sql.NullString, resourceType sql.NullString, action sql.NullString, effect sql.NullString, conditions sql.NullString, priority sql.NullInt32, createdAt sql.NullTime, updatedAt sql.NullTime) *Policy {
	p := &Policy{}
	if id.Valid {
		p.ID = id.String
	}
	if orgID.Valid {
		p.OrganizationID = orgID.String
	}
	if projectID.Valid {
		pid := projectID.String
		p.ProjectID = &pid
	}
	if name.Valid {
		p.Name = name.String
	}
	if resourceType.Valid {
		p.ResourceType = resourceType.String
	}
	if action.Valid {
		p.Action = action.String
	}
	if effect.Valid {
		p.Effect = effect.String
	}
	if conditions.Valid {
		p.Conditions = json.RawMessage(conditions.String)
	}
	if priority.Valid {
		p.Priority = int(priority.Int32)
	}
	if createdAt.Valid {
		p.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		p.UpdatedAt = updatedAt.Time
	}
	return p
}
