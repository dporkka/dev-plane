package models

import (
	"database/sql"
	"encoding/json"
	"time"
)

// AgentRunStatus constants for agent run lifecycle.
const (
	AgentRunStatusPending   = "pending"
	AgentRunStatusQueued    = "queued"
	AgentRunStatusRunning   = "running"
	AgentRunStatusPaused    = "paused"
	AgentRunStatusCompleted = "completed"
	AgentRunStatusFailed    = "failed"
	AgentRunStatusCancelled = "cancelled"
)

// AgentStepType constants for the type of step an agent performed.
const (
	AgentStepTypeThought         = "thought"
	AgentStepTypeToolCall        = "tool_call"
	AgentStepTypeCommandRun      = "command_run"
	AgentStepTypeFilePatch       = "file_patch"
	AgentStepTypeApprovalRequest = "approval_request"
	AgentStepTypeMessage         = "message"
	AgentStepTypeError           = "error"
)

// AgentStepStatus constants for individual step states.
const (
	AgentStepStatusPending   = "pending"
	AgentStepStatusRunning   = "running"
	AgentStepStatusCompleted = "completed"
	AgentStepStatusFailed    = "failed"
)

// AgentRole constants for the type of agent performing work.
const (
	AgentRolePlanner        = "planner"
	AgentRoleImplementer    = "implementer"
	AgentRoleReviewer       = "reviewer"
	AgentRoleTestRunner     = "test_runner"
	AgentRoleSecurity       = "security_reviewer"
	AgentRoleDocs           = "docs_writer"
	AgentRoleReleaseManager = "release_manager"
)

// AgentRun represents a single execution of an agent on a task.
type AgentRun struct {
	ID               string          `json:"id"`
	TaskID           string          `json:"task_id"`
	WorkspaceID      *string         `json:"workspace_id,omitempty"`
	AgentRole        string          `json:"agent_role"`
	Model            *string         `json:"model,omitempty"`
	Provider         *string         `json:"provider,omitempty"`
	Status           string          `json:"status"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TotalCost        float64         `json:"total_cost"`
	ErrorMessage     *string         `json:"error_message,omitempty"`
	Summary          *string         `json:"summary,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// AgentStep represents a single step within an agent run.
type AgentStep struct {
	ID            string          `json:"id"`
	AgentRunID    string          `json:"agent_run_id"`
	StepNumber    int             `json:"step_number"`
	StepType      string          `json:"step_type"`
	Status        string          `json:"status"`
	Content       *string         `json:"content,omitempty"`
	ToolName      *string         `json:"tool_name,omitempty"`
	ToolInput     json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput    json.RawMessage `json:"tool_output,omitempty"`
	Command       *string         `json:"command,omitempty"`
	CommandOutput *string         `json:"command_output,omitempty"`
	ExitCode      *int            `json:"exit_code,omitempty"`
	FilePath      *string         `json:"file_path,omitempty"`
	Diff          *string         `json:"diff,omitempty"`
	Cost          float64         `json:"cost"`
	LatencyMs     int             `json:"latency_ms"`
	CreatedAt     time.Time       `json:"created_at"`
}

// NullAgentRun returns an AgentRun from sql.Null fields.
func NullAgentRun(id sql.NullString, taskID sql.NullString, workspaceID sql.NullString, agentRole sql.NullString, model sql.NullString, provider sql.NullString, status sql.NullString, startedAt sql.NullTime, completedAt sql.NullTime, promptTokens sql.NullInt32, completionTokens sql.NullInt32, totalCost sql.NullFloat64, errorMessage sql.NullString, summary sql.NullString, metadata sql.NullString, createdAt sql.NullTime, updatedAt sql.NullTime) *AgentRun {
	r := &AgentRun{}
	if id.Valid {
		r.ID = id.String
	}
	if taskID.Valid {
		r.TaskID = taskID.String
	}
	if workspaceID.Valid {
		w := workspaceID.String
		r.WorkspaceID = &w
	}
	if agentRole.Valid {
		r.AgentRole = agentRole.String
	}
	if model.Valid {
		m := model.String
		r.Model = &m
	}
	if provider.Valid {
		p := provider.String
		r.Provider = &p
	}
	if status.Valid {
		r.Status = status.String
	}
	if startedAt.Valid {
		r.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		r.CompletedAt = &completedAt.Time
	}
	if promptTokens.Valid {
		r.PromptTokens = int(promptTokens.Int32)
	}
	if completionTokens.Valid {
		r.CompletionTokens = int(completionTokens.Int32)
	}
	if totalCost.Valid {
		r.TotalCost = totalCost.Float64
	}
	if errorMessage.Valid {
		e := errorMessage.String
		r.ErrorMessage = &e
	}
	if summary.Valid {
		s := summary.String
		r.Summary = &s
	}
	if metadata.Valid {
		r.Metadata = json.RawMessage(metadata.String)
	}
	if createdAt.Valid {
		r.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		r.UpdatedAt = updatedAt.Time
	}
	return r
}

// NullAgentStep returns an AgentStep from sql.Null fields.
func NullAgentStep(id sql.NullString, agentRunID sql.NullString, stepNumber sql.NullInt32, stepType sql.NullString, status sql.NullString, content sql.NullString, toolName sql.NullString, toolInput sql.NullString, toolOutput sql.NullString, command sql.NullString, commandOutput sql.NullString, exitCode sql.NullInt32, filePath sql.NullString, diff sql.NullString, cost sql.NullFloat64, latencyMs sql.NullInt32, createdAt sql.NullTime) *AgentStep {
	s := &AgentStep{}
	if id.Valid {
		s.ID = id.String
	}
	if agentRunID.Valid {
		s.AgentRunID = agentRunID.String
	}
	if stepNumber.Valid {
		s.StepNumber = int(stepNumber.Int32)
	}
	if stepType.Valid {
		s.StepType = stepType.String
	}
	if status.Valid {
		s.Status = status.String
	}
	if content.Valid {
		c := content.String
		s.Content = &c
	}
	if toolName.Valid {
		tn := toolName.String
		s.ToolName = &tn
	}
	if toolInput.Valid {
		s.ToolInput = json.RawMessage(toolInput.String)
	}
	if toolOutput.Valid {
		s.ToolOutput = json.RawMessage(toolOutput.String)
	}
	if command.Valid {
		c := command.String
		s.Command = &c
	}
	if commandOutput.Valid {
		co := commandOutput.String
		s.CommandOutput = &co
	}
	if exitCode.Valid {
		ec := int(exitCode.Int32)
		s.ExitCode = &ec
	}
	if filePath.Valid {
		fp := filePath.String
		s.FilePath = &fp
	}
	if diff.Valid {
		d := diff.String
		s.Diff = &d
	}
	if cost.Valid {
		s.Cost = cost.Float64
	}
	if latencyMs.Valid {
		s.LatencyMs = int(latencyMs.Int32)
	}
	if createdAt.Valid {
		s.CreatedAt = createdAt.Time
	}
	return s
}
