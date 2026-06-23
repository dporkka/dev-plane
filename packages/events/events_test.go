package events

import (
	"encoding/json"
	"testing"
)

func TestTaskEvent_MarshalUnmarshal(t *testing.T) {
	event := TaskEvent{
		TaskID:    "task-1",
		Status:    "completed",
		ProjectID: "proj-1",
		ActorID:   "user-1",
		Data:      json.RawMessage(`{"key":"value"}`),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal task event: %v", err)
	}

	var decoded TaskEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal task event: %v", err)
	}

	if decoded.TaskID != event.TaskID {
		t.Errorf("task_id = %q, want %q", decoded.TaskID, event.TaskID)
	}
	if decoded.Status != event.Status {
		t.Errorf("status = %q, want %q", decoded.Status, event.Status)
	}
	if decoded.ProjectID != event.ProjectID {
		t.Errorf("project_id = %q, want %q", decoded.ProjectID, event.ProjectID)
	}
	if decoded.ActorID != event.ActorID {
		t.Errorf("actor_id = %q, want %q", decoded.ActorID, event.ActorID)
	}
	if string(decoded.Data) != string(event.Data) {
		t.Errorf("data = %q, want %q", decoded.Data, event.Data)
	}
}

func TestAgentRunEvent_MarshalUnmarshal(t *testing.T) {
	event := AgentRunEvent{
		RunID:     "run-1",
		TaskID:    "task-1",
		AgentRole: "implementer",
		Status:    "completed",
		Data:      json.RawMessage(`{"steps":5}`),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal agent run event: %v", err)
	}

	var decoded AgentRunEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal agent run event: %v", err)
	}

	if decoded.RunID != event.RunID {
		t.Errorf("run_id = %q, want %q", decoded.RunID, event.RunID)
	}
	if decoded.AgentRole != event.AgentRole {
		t.Errorf("agent_role = %q, want %q", decoded.AgentRole, event.AgentRole)
	}
	if string(decoded.Data) != string(event.Data) {
		t.Errorf("data = %q, want %q", decoded.Data, event.Data)
	}
}

func TestAgentStepEvent_MarshalUnmarshal(t *testing.T) {
	event := AgentStepEvent{
		StepID:   "step-1",
		RunID:    "run-1",
		TaskID:   "task-1",
		StepType: "tool_call",
		Status:   "completed",
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal agent step event: %v", err)
	}

	var decoded AgentStepEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal agent step event: %v", err)
	}

	if decoded.StepID != event.StepID {
		t.Errorf("step_id = %q, want %q", decoded.StepID, event.StepID)
	}
	if decoded.StepType != event.StepType {
		t.Errorf("step_type = %q, want %q", decoded.StepType, event.StepType)
	}
}

func TestWebhookEvent_MarshalUnmarshal(t *testing.T) {
	event := WebhookEvent{
		Source:       "github",
		EventType:    "push",
		DeliveryID:   "delivery-1",
		RepositoryID: "repo-1",
		Payload:      json.RawMessage(`{"ref":"refs/heads/main"}`),
		Signature:    "sha256=abc123",
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal webhook event: %v", err)
	}

	var decoded WebhookEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal webhook event: %v", err)
	}

	if decoded.Source != event.Source {
		t.Errorf("source = %q, want %q", decoded.Source, event.Source)
	}
	if decoded.EventType != event.EventType {
		t.Errorf("event_type = %q, want %q", decoded.EventType, event.EventType)
	}
	if decoded.DeliveryID != event.DeliveryID {
		t.Errorf("delivery_id = %q, want %q", decoded.DeliveryID, event.DeliveryID)
	}
	if decoded.RepositoryID != event.RepositoryID {
		t.Errorf("repository_id = %q, want %q", decoded.RepositoryID, event.RepositoryID)
	}
	if string(decoded.Payload) != string(event.Payload) {
		t.Errorf("payload = %q, want %q", decoded.Payload, event.Payload)
	}
	if decoded.Signature != event.Signature {
		t.Errorf("signature = %q, want %q", decoded.Signature, event.Signature)
	}
}

func TestAuditEvent_MarshalUnmarshal(t *testing.T) {
	event := AuditEvent{
		OrganizationID: "org-1",
		ActorType:      "user",
		ActorID:        "user-1",
		Action:         "task.created",
		ResourceType:   "task",
		ResourceID:     "task-1",
		Details:        json.RawMessage(`{"ip":"127.0.0.1"}`),
		IPAddress:      "127.0.0.1",
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal audit event: %v", err)
	}

	var decoded AuditEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal audit event: %v", err)
	}

	if decoded.OrganizationID != event.OrganizationID {
		t.Errorf("organization_id = %q, want %q", decoded.OrganizationID, event.OrganizationID)
	}
	if decoded.Action != event.Action {
		t.Errorf("action = %q, want %q", decoded.Action, event.Action)
	}
	if decoded.IPAddress != event.IPAddress {
		t.Errorf("ip_address = %q, want %q", decoded.IPAddress, event.IPAddress)
	}
}

func TestRunEvent_MarshalUnmarshal(t *testing.T) {
	event := RunEvent{
		RunID:    "run-1",
		TaskID:   "task-1",
		Status:   "finished",
		WorkerID: "worker-1",
		Data:     json.RawMessage(`{"duration":120}`),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal run event: %v", err)
	}

	var decoded RunEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal run event: %v", err)
	}

	if decoded.RunID != event.RunID {
		t.Errorf("run_id = %q, want %q", decoded.RunID, event.RunID)
	}
	if decoded.WorkerID != event.WorkerID {
		t.Errorf("worker_id = %q, want %q", decoded.WorkerID, event.WorkerID)
	}
}

func TestEventConstants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{TaskCreated, "tasks.created"},
		{TaskUpdated, "tasks.updated"},
		{TaskApproved, "tasks.approved"},
		{TaskStarted, "tasks.started"},
		{TaskCompleted, "tasks.completed"},
		{TaskFailed, "tasks.failed"},
		{TaskCancelled, "tasks.cancelled"},
		{AgentRunStarted, "agents.run.started"},
		{AgentRunCompleted, "agents.run.completed"},
		{AgentRunFailed, "agents.run.failed"},
		{RunTriggered, "runs.triggered"},
		{WebhookReceived, "webhooks.received"},
		{AuditActionLogged, "audit.action.logged"},
		{ReviewTriggered, "review.triggered"},
		{ApprovalRequested, "approval.requested"},
		{PRCreated, "pr.created"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
