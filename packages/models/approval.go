package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ApprovalType constants.
const (
	ApprovalTypeSpec        = "spec"
	ApprovalTypeExecution   = "execution"
	ApprovalTypePRCreate    = "pr_create"
	ApprovalTypeDeploy      = "deploy"
	ApprovalTypeRiskyAction = "risky_action"
)

// ApprovalResponse constants.
const (
	ApprovalResponseApproved = "approved"
	ApprovalResponseRejected = "rejected"
)

// Approval represents a human approval request for a task or action.
type Approval struct {
	ID           string          `json:"id"`
	TaskID       string          `json:"task_id"`
	AgentRunID   *string         `json:"agent_run_id,omitempty"`
	ApprovalType string          `json:"approval_type"`
	RequestedBy  string          `json:"requested_by"`
	RequestedAt  time.Time       `json:"requested_at"`
	RespondedBy  *string         `json:"responded_by,omitempty"`
	Response     *string         `json:"response,omitempty"`
	ResponseNote *string         `json:"response_note,omitempty"`
	RespondedAt  *time.Time      `json:"responded_at,omitempty"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// Validate checks that the approval has required fields.
func (a *Approval) Validate() error {
	if a.TaskID == "" {
		return errors.New("approval task_id is required")
	}
	if a.ApprovalType == "" {
		return errors.New("approval approval_type is required")
	}
	if a.RequestedBy == "" {
		return errors.New("approval requested_by is required")
	}
	return nil
}

// IsPending returns true if the approval has not been responded to.
func (a *Approval) IsPending() bool {
	return a.Response == nil
}

// IsExpired returns true if the approval has passed its expiration time.
func (a *Approval) IsExpired() bool {
	if a.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*a.ExpiresAt)
}

// NullApproval returns an Approval from sql.Null fields.
func NullApproval(id sql.NullString, taskID sql.NullString, agentRunID sql.NullString, approvalType sql.NullString, requestedBy sql.NullString, requestedAt sql.NullTime, respondedBy sql.NullString, response sql.NullString, responseNote sql.NullString, respondedAt sql.NullTime, expiresAt sql.NullTime, metadata sql.NullString, createdAt sql.NullTime, updatedAt sql.NullTime) *Approval {
	a := &Approval{}
	if id.Valid {
		a.ID = id.String
	}
	if taskID.Valid {
		a.TaskID = taskID.String
	}
	if agentRunID.Valid {
		ar := agentRunID.String
		a.AgentRunID = &ar
	}
	if approvalType.Valid {
		a.ApprovalType = approvalType.String
	}
	if requestedBy.Valid {
		a.RequestedBy = requestedBy.String
	}
	if requestedAt.Valid {
		a.RequestedAt = requestedAt.Time
	}
	if respondedBy.Valid {
		rb := respondedBy.String
		a.RespondedBy = &rb
	}
	if response.Valid {
		r := response.String
		a.Response = &r
	}
	if responseNote.Valid {
		rn := responseNote.String
		a.ResponseNote = &rn
	}
	if respondedAt.Valid {
		a.RespondedAt = &respondedAt.Time
	}
	if expiresAt.Valid {
		a.ExpiresAt = &expiresAt.Time
	}
	if metadata.Valid {
		a.Metadata = json.RawMessage(metadata.String)
	}
	if createdAt.Valid {
		a.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		a.UpdatedAt = updatedAt.Time
	}
	return a
}
