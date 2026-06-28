package main

// Minimal API response types used by the CLI. These mirror the public API
// contract without importing the larger backend model packages.

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Task struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	CreatedAt   string `json:"created_at"`
}

type CreateTaskRequest struct {
	RepositoryID string `json:"repository_id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
}

type AgentRun struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	AgentRole string `json:"agent_role"`
	CreatedAt string `json:"created_at"`
}

type RunEvent struct {
	ID          string `json:"id"`
	AgentRunID  string `json:"agent_run_id"`
	EventType   string `json:"event_type"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	CreatedAt   string `json:"created_at"`
}

type RunStreamEvent struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

type Approval struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	ApprovalType string `json:"approval_type"`
	RequestedBy  string `json:"requested_by"`
	Response     string `json:"response"`
	CreatedAt    string `json:"created_at"`
}

type RespondApprovalRequest struct {
	Response string `json:"response"`
}

type StatusResponse struct {
	Status string `json:"status"`
	ID     string `json:"id"`
}
