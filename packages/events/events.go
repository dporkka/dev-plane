package events

import "encoding/json"

// Task event subject constants.
const (
	TaskCreated   = "tasks.created"
	TaskUpdated   = "tasks.updated"
	TaskApproved  = "tasks.approved"
	TaskStarted   = "tasks.started"
	TaskCompleted = "tasks.completed"
	TaskFailed    = "tasks.failed"
	TaskCancelled = "tasks.cancelled"
)

// Agent event subject constants.
const (
	AgentRunStarted    = "agents.run.started"
	AgentRunCompleted  = "agents.run.completed"
	AgentRunFailed     = "agents.run.failed"
	AgentRunCancelled  = "agents.run.cancelled"
	AgentStepCreated   = "agents.step.created"
	AgentStepCompleted = "agents.step.completed"
	AgentStepFailed    = "agents.step.failed"
)

// Run event subject constants.
const (
	RunTriggered = "runs.triggered"
	RunAssigned  = "runs.assigned"
	RunFinished  = "runs.finished"
)

// Webhook event subject constants.
const (
	WebhookReceived    = "webhooks.received"
	WebhookProcessed   = "webhooks.processed"
	WebhookFailed      = "webhooks.failed"
)

// Audit event subject constants.
const (
	AuditActionLogged = "audit.action.logged"
)

// Review event subject constants.
const (
	ReviewTriggered = "review.triggered"
	ReviewCompleted = "review.completed"
)

// Approval event subject constants.
const (
	ApprovalRequested = "approval.requested"
	ApprovalApproved  = "approval.approved"
	ApprovalRejected  = "approval.rejected"
)

// PR event subject constants.
const (
	PRCreated = "pr.created"
)

// TaskEvent is the payload for task lifecycle events.
type TaskEvent struct {
	TaskID    string          `json:"task_id"`
	Status    string          `json:"status"`
	ProjectID string          `json:"project_id"`
	ActorID   string          `json:"actor_id,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// AgentRunEvent is the payload for agent run lifecycle events.
type AgentRunEvent struct {
	RunID     string          `json:"run_id"`
	TaskID    string          `json:"task_id"`
	AgentRole string          `json:"agent_role"`
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// AgentStepEvent is the payload for agent step events.
type AgentStepEvent struct {
	StepID     string          `json:"step_id"`
	RunID      string          `json:"run_id"`
	TaskID     string          `json:"task_id"`
	StepType   string          `json:"step_type"`
	Status     string          `json:"status"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// WebhookEvent is the payload for incoming webhook events.
type WebhookEvent struct {
	Source      string          `json:"source"` // github, linear, slack, etc.
	EventType   string          `json:"event_type"`
	DeliveryID  string          `json:"delivery_id"`
	RepositoryID string         `json:"repository_id,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	Signature   string          `json:"signature,omitempty"`
}

// AuditEvent is the payload for audit log events.
type AuditEvent struct {
	OrganizationID string          `json:"organization_id"`
	ActorType      string          `json:"actor_type"`
	ActorID        string          `json:"actor_id,omitempty"`
	Action         string          `json:"action"`
	ResourceType   string          `json:"resource_type"`
	ResourceID     string          `json:"resource_id,omitempty"`
	Details        json.RawMessage `json:"details,omitempty"`
	IPAddress      string          `json:"ip_address,omitempty"`
}

// RunEvent is the payload for background run events.
type RunEvent struct {
	RunID    string          `json:"run_id"`
	TaskID   string          `json:"task_id"`
	Status   string          `json:"status"`
	WorkerID string          `json:"worker_id,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}
