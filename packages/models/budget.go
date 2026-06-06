package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// BudgetType constants for different budget scopes.
const (
	BudgetTypeOrganization = "organization"
	BudgetTypeProject      = "project"
	BudgetTypeTask         = "task"
)

// BudgetPeriod constants for budget reset cadence.
const (
	BudgetPeriodDaily   = "daily"
	BudgetPeriodWeekly  = "weekly"
	BudgetPeriodMonthly = "monthly"
	BudgetPeriodPerRun  = "per_run"
)

// Budget represents spending and usage constraints for an organization, project, or task.
type Budget struct {
	ID               string          `json:"id"`
	OrganizationID   string          `json:"organization_id"`
	ProjectID        *string         `json:"project_id,omitempty"`
	TaskID           *string         `json:"task_id,omitempty"`
	Type             string          `json:"type"`
	Period           string          `json:"period"`
	MaxCost          *float64        `json:"max_cost,omitempty"`
	MaxRuntimeMinutes int            `json:"max_runtime_minutes"`
	MaxModelCalls    int             `json:"max_model_calls"`
	MaxToolCalls     int             `json:"max_tool_calls"`
	MaxShellCommands int             `json:"max_shell_commands"`
	MaxConcurrentAgents int          `json:"max_concurrent_agents"`
	MaxDailySpend    *float64        `json:"max_daily_spend,omitempty"`
	Notifications    json.RawMessage `json:"notifications,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// Validate checks that the budget has required fields.
func (b *Budget) Validate() error {
	if b.OrganizationID == "" {
		return errors.New("budget organization_id is required")
	}
	if b.Type == "" {
		return errors.New("budget type is required")
	}
	if b.Type != BudgetTypeOrganization && b.Type != BudgetTypeProject && b.Type != BudgetTypeTask {
		return errors.New("invalid budget type")
	}
	if b.Period == "" {
		return errors.New("budget period is required")
	}
	return nil
}

// IsUnlimited returns true if the budget has no meaningful constraints.
func (b *Budget) IsUnlimited() bool {
	if b == nil {
		return true
	}
	return b.MaxCost == nil && b.MaxDailySpend == nil &&
		b.MaxRuntimeMinutes == 0 && b.MaxModelCalls == 0 &&
		b.MaxToolCalls == 0 && b.MaxShellCommands == 0 &&
		b.MaxConcurrentAgents == 0
}

// NullBudget returns a Budget from sql.Null fields.
func NullBudget(id sql.NullString, orgID sql.NullString, projectID sql.NullString, taskID sql.NullString, budgetType sql.NullString, period sql.NullString, maxCost sql.NullFloat64, maxRuntime sql.NullInt32, maxModelCalls sql.NullInt32, maxToolCalls sql.NullInt32, maxShellCommands sql.NullInt32, maxConcurrentAgents sql.NullInt32, maxDailySpend sql.NullFloat64, notifications sql.NullString, createdAt sql.NullTime, updatedAt sql.NullTime) *Budget {
	b := &Budget{}
	if id.Valid {
		b.ID = id.String
	}
	if orgID.Valid {
		b.OrganizationID = orgID.String
	}
	if projectID.Valid {
		p := projectID.String
		b.ProjectID = &p
	}
	if taskID.Valid {
		t := taskID.String
		b.TaskID = &t
	}
	if budgetType.Valid {
		b.Type = budgetType.String
	}
	if period.Valid {
		b.Period = period.String
	}
	if maxCost.Valid {
		b.MaxCost = &maxCost.Float64
	}
	if maxRuntime.Valid {
		b.MaxRuntimeMinutes = int(maxRuntime.Int32)
	}
	if maxModelCalls.Valid {
		b.MaxModelCalls = int(maxModelCalls.Int32)
	}
	if maxToolCalls.Valid {
		b.MaxToolCalls = int(maxToolCalls.Int32)
	}
	if maxShellCommands.Valid {
		b.MaxShellCommands = int(maxShellCommands.Int32)
	}
	if maxConcurrentAgents.Valid {
		b.MaxConcurrentAgents = int(maxConcurrentAgents.Int32)
	}
	if maxDailySpend.Valid {
		b.MaxDailySpend = &maxDailySpend.Float64
	}
	if notifications.Valid {
		b.Notifications = json.RawMessage(notifications.String)
	}
	if createdAt.Valid {
		b.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		b.UpdatedAt = updatedAt.Time
	}
	return b
}
