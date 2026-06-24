package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// TaskStatus represents the lifecycle state of a task.
type TaskStatus string

// TaskStatus constants.
const (
	TaskStatusBacklog    TaskStatus = "backlog"
	TaskStatusSpecReview TaskStatus = "spec_review"
	TaskStatusApproved   TaskStatus = "approved"
	TaskStatusRunning    TaskStatus = "running"
	TaskStatusReviewing  TaskStatus = "reviewing"
	TaskStatusPRCreated  TaskStatus = "pr_created"
	TaskStatusDeploying  TaskStatus = "deploying"
	TaskStatusDone       TaskStatus = "done"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusCancelled  TaskStatus = "cancelled"
)

// Priority represents task priority levels.
type Priority string

// Priority constants.
const (
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

// RiskLevel represents the risk assessment of a task.
type RiskLevel string

// RiskLevel constants.
const (
	RiskLevelLow      RiskLevel = "low"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
)

// TaskSource represents where a task originated.
const (
	TaskSourceWeb     = "web"
	TaskSourceGitHub  = "github_issue"
	TaskSourceLinear  = "linear"
	TaskSourceSlack   = "slack"
	TaskSourceDiscord = "discord"
	TaskSourceWebhook = "webhook"
	TaskSourceVoice   = "voice"
)

// Task represents a unit of work to be performed by agents.
type Task struct {
	ID                   string          `json:"id"`
	ProjectID            string          `json:"project_id"`
	RepositoryID         string          `json:"repository_id"`
	WorkspaceID          *string         `json:"workspace_id,omitempty"`
	CreatedBy            string          `json:"created_by"`
	Source               string          `json:"source"`
	SourceID             *string         `json:"source_id,omitempty"`
	Title                string          `json:"title"`
	Description          *string         `json:"description,omitempty"`
	Status               TaskStatus      `json:"status"`
	Priority             Priority        `json:"priority"`
	RiskLevel            RiskLevel       `json:"risk_level"`
	TargetBranch         string          `json:"target_branch"`
	Spec                 json.RawMessage `json:"spec,omitempty"`
	AcceptanceCriteria   json.RawMessage `json:"acceptance_criteria,omitempty"`
	MaxCost              *float64        `json:"max_cost,omitempty"`
	MaxRuntimeMinutes    int             `json:"max_runtime_minutes"`
	ApprovalRequirements json.RawMessage `json:"approval_requirements,omitempty"`
	Metadata             json.RawMessage `json:"metadata,omitempty"`
	StartedAt            *time.Time      `json:"started_at,omitempty"`
	CompletedAt          *time.Time      `json:"completed_at,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	DeletedAt            *time.Time      `json:"deleted_at,omitempty"`
}

// Validate checks that the task has required fields.
func (t *Task) Validate() error {
	if t.Title == "" {
		return errors.New("task title is required")
	}
	if t.ProjectID == "" {
		return errors.New("task project_id is required")
	}
	if t.RepositoryID == "" {
		return errors.New("task repository_id is required")
	}
	if t.CreatedBy == "" {
		return errors.New("task created_by is required")
	}
	return nil
}

// IsTerminal returns true if the task is in a terminal state.
func (t *Task) IsTerminal() bool {
	return t.Status == TaskStatusDone || t.Status == TaskStatusFailed || t.Status == TaskStatusCancelled
}

// IsActive returns true if the task is currently being worked on.
func (t *Task) IsActive() bool {
	return t.Status == TaskStatusRunning || t.Status == TaskStatusReviewing
}

// NullTask returns a Task from sql.Null fields.
func NullTask(id sql.NullString, projectID sql.NullString, repoID sql.NullString, workspaceID sql.NullString, createdBy sql.NullString, source sql.NullString, sourceID sql.NullString, title sql.NullString, description sql.NullString, status sql.NullString, priority sql.NullString, riskLevel sql.NullString, targetBranch sql.NullString, spec sql.NullString, acceptanceCriteria sql.NullString, maxCost sql.NullFloat64, maxRuntime sql.NullInt32, approvalReqs sql.NullString, metadata sql.NullString, startedAt sql.NullTime, completedAt sql.NullTime, createdAt sql.NullTime, updatedAt sql.NullTime, deletedAt sql.NullTime) *Task {
	t := &Task{}
	if id.Valid {
		t.ID = id.String
	}
	if projectID.Valid {
		t.ProjectID = projectID.String
	}
	if repoID.Valid {
		t.RepositoryID = repoID.String
	}
	if workspaceID.Valid {
		w := workspaceID.String
		t.WorkspaceID = &w
	}
	if createdBy.Valid {
		t.CreatedBy = createdBy.String
	}
	if source.Valid {
		t.Source = source.String
	}
	if sourceID.Valid {
		s := sourceID.String
		t.SourceID = &s
	}
	if title.Valid {
		t.Title = title.String
	}
	if description.Valid {
		d := description.String
		t.Description = &d
	}
	if status.Valid {
		t.Status = TaskStatus(status.String)
	}
	if priority.Valid {
		t.Priority = Priority(priority.String)
	}
	if riskLevel.Valid {
		t.RiskLevel = RiskLevel(riskLevel.String)
	}
	if targetBranch.Valid {
		t.TargetBranch = targetBranch.String
	}
	if spec.Valid {
		t.Spec = json.RawMessage(spec.String)
	}
	if acceptanceCriteria.Valid {
		t.AcceptanceCriteria = json.RawMessage(acceptanceCriteria.String)
	}
	if maxCost.Valid {
		c := maxCost.Float64
		t.MaxCost = &c
	}
	if maxRuntime.Valid {
		t.MaxRuntimeMinutes = int(maxRuntime.Int32)
	}
	if approvalReqs.Valid {
		t.ApprovalRequirements = json.RawMessage(approvalReqs.String)
	}
	if metadata.Valid {
		t.Metadata = json.RawMessage(metadata.String)
	}
	if startedAt.Valid {
		t.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		t.CompletedAt = &completedAt.Time
	}
	if createdAt.Valid {
		t.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		t.UpdatedAt = updatedAt.Time
	}
	if deletedAt.Valid {
		t.DeletedAt = &deletedAt.Time
	}
	return t
}
