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
