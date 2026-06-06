package agents

import (
	"context"
	"time"

	"github.com/ai-dev-control-plane/models"
)

// AgentRole identifies the type of agent.
type AgentRole string

// AgentRole constants for all supported agent types.
const (
	RolePlanner         AgentRole = "planner"
	RoleImplementer     AgentRole = "implementer"
	RoleReviewer        AgentRole = "reviewer"
	RoleTestRunner      AgentRole = "test_runner"
	RoleSecurity        AgentRole = "security_reviewer"
	RoleDocs            AgentRole = "docs_writer"
	RoleReleaseManager  AgentRole = "release_manager"
)

// String returns the string representation of the role.
func (r AgentRole) String() string {
	return string(r)
}

// Valid returns true if the role is a known agent role.
func (r AgentRole) Valid() bool {
	switch r {
	case RolePlanner, RoleImplementer, RoleReviewer, RoleTestRunner,
		RoleSecurity, RoleDocs, RoleReleaseManager:
		return true
	}
	return false
}

// TokenUsage tracks LLM token consumption.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// AgentStepResult represents the outcome of a single execution step.
type AgentStepResult struct {
	Type      string        `json:"type"`
	Content   string        `json:"content"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	Cost      float64       `json:"cost"`
	LatencyMs int           `json:"latency_ms"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// AgentResult is the aggregate result of an agent execution.
type AgentResult struct {
	Success bool              `json:"success"`
	Summary string            `json:"summary"`
	Steps   []AgentStepResult `json:"steps"`
	Cost    float64           `json:"cost"`
	Tokens  TokenUsage        `json:"tokens"`
	Duration time.Duration    `json:"duration_ms"`
}

// Agent defines the interface that all agent types must implement.
type Agent interface {
	// Role returns the agent's role identifier.
	Role() AgentRole

	// Execute runs the agent against the given task and workspace.
	// The workspace provides the isolated environment (files, git, commands).
	Execute(ctx context.Context, task *models.Task, workspace *models.Workspace) (*AgentResult, error)

	// Tools returns the set of tools available to this agent.
	Tools() []Tool

	// SystemPrompt returns the system prompt for this agent role.
	SystemPrompt() string
}

// AgentFactory creates Agent instances for given roles.
type AgentFactory interface {
	Create(role AgentRole) (Agent, error)
}

// Runner orchestrates agent execution.
type Runner struct {
	factory AgentFactory
}

// NewRunner creates a new agent runner.
func NewRunner(factory AgentFactory) *Runner {
	return &Runner{factory: factory}
}

// Run executes a single agent for the given role against a task.
func (r *Runner) Run(ctx context.Context, role AgentRole, task *models.Task, workspace *models.Workspace) (*AgentResult, error) {
	agent, err := r.factory.Create(role)
	if err != nil {
		return nil, err
	}
	return agent.Execute(ctx, task, workspace)
}
