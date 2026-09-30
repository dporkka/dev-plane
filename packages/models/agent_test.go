package models

import "testing"

func TestAgentRunStatus_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{AgentRunStatusPending, "pending"},
		{AgentRunStatusQueued, "queued"},
		{AgentRunStatusRunning, "running"},
		{AgentRunStatusPaused, "paused"},
		{AgentRunStatusCompleted, "completed"},
		{AgentRunStatusFailed, "failed"},
		{AgentRunStatusCancelled, "cancelled"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestAgentStepType_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{AgentStepTypeThought, "thought"},
		{AgentStepTypeToolCall, "tool_call"},
		{AgentStepTypeCommandRun, "command_run"},
		{AgentStepTypeFilePatch, "file_patch"},
		{AgentStepTypeApprovalRequest, "approval_request"},
		{AgentStepTypeMessage, "message"},
		{AgentStepTypeError, "error"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestAgentStepStatus_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{AgentStepStatusPending, "pending"},
		{AgentStepStatusRunning, "running"},
		{AgentStepStatusCompleted, "completed"},
		{AgentStepStatusFailed, "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestAgentRole_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{AgentRolePlanner, "planner"},
		{AgentRoleImplementer, "implementer"},
		{AgentRoleReviewer, "reviewer"},
		{AgentRoleTestRunner, "test_runner"},
		{AgentRoleSecurity, "security_reviewer"},
		{AgentRoleDocs, "docs_writer"},
		{AgentRoleReleaseManager, "release_manager"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}


func TestCanTransitionAgentRunStatus(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{name: "pending to queued", from: AgentRunStatusPending, to: AgentRunStatusQueued, want: true},
		{name: "pending direct to running", from: AgentRunStatusPending, to: AgentRunStatusRunning, want: true},
		{name: "queued to admitting", from: AgentRunStatusQueued, to: AgentRunStatusAdmitting, want: true},
		{name: "admitting back to queued", from: AgentRunStatusAdmitting, to: AgentRunStatusQueued, want: true},
		{name: "admitting to running", from: AgentRunStatusAdmitting, to: AgentRunStatusRunning, want: true},
		{name: "running to paused", from: AgentRunStatusRunning, to: AgentRunStatusPaused, want: true},
		{name: "paused to queued", from: AgentRunStatusPaused, to: AgentRunStatusQueued, want: true},
		{name: "running to completed", from: AgentRunStatusRunning, to: AgentRunStatusCompleted, want: true},
		{name: "running to failed", from: AgentRunStatusRunning, to: AgentRunStatusFailed, want: true},
		{name: "completed cannot regress", from: AgentRunStatusCompleted, to: AgentRunStatusRunning, want: false},
		{name: "failed cannot restart", from: AgentRunStatusFailed, to: AgentRunStatusQueued, want: false},
		{name: "cancelled cannot fail", from: AgentRunStatusCancelled, to: AgentRunStatusFailed, want: false},
		{name: "same state is not a transition", from: AgentRunStatusRunning, to: AgentRunStatusRunning, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanTransitionAgentRunStatus(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanTransitionAgentRunStatus(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}
