package models

import (
	"database/sql"
	"testing"
	"time"
)

func TestNullAgentRun(t *testing.T) {
	now := time.Now()
	run := NullAgentRun(
		sql.NullString{String: "run-1", Valid: true},
		sql.NullString{String: "task-1", Valid: true},
		sql.NullString{String: "ws-1", Valid: true},
		sql.NullString{String: "implementer", Valid: true},
		sql.NullString{String: "gpt-4", Valid: true},
		sql.NullString{String: "openai", Valid: true},
		sql.NullString{String: AgentRunStatusCompleted, Valid: true},
		sql.NullTime{Time: now, Valid: true},
		sql.NullTime{Time: now, Valid: true},
		sql.NullInt32{Int32: 100, Valid: true},
		sql.NullInt32{Int32: 50, Valid: true},
		sql.NullFloat64{Float64: 0.123, Valid: true},
		sql.NullString{String: "error", Valid: true},
		sql.NullString{String: "summary", Valid: true},
		sql.NullString{String: `{"key":"value"}`, Valid: true},
		sql.NullTime{Time: now, Valid: true},
		sql.NullTime{Time: now, Valid: true},
	)

	if run.ID != "run-1" {
		t.Errorf("id = %q", run.ID)
	}
	if run.TaskID != "task-1" {
		t.Errorf("task_id = %q", run.TaskID)
	}
	if run.WorkspaceID == nil || *run.WorkspaceID != "ws-1" {
		t.Errorf("workspace_id = %v", run.WorkspaceID)
	}
	if run.Model == nil || *run.Model != "gpt-4" {
		t.Errorf("model = %v", run.Model)
	}
	if run.Provider == nil || *run.Provider != "openai" {
		t.Errorf("provider = %v", run.Provider)
	}
	if run.PromptTokens != 100 {
		t.Errorf("prompt_tokens = %d", run.PromptTokens)
	}
	if run.CompletionTokens != 50 {
		t.Errorf("completion_tokens = %d", run.CompletionTokens)
	}
	if run.TotalCost != 0.123 {
		t.Errorf("total_cost = %f", run.TotalCost)
	}
	if run.ErrorMessage == nil || *run.ErrorMessage != "error" {
		t.Errorf("error_message = %v", run.ErrorMessage)
	}
	if run.Summary == nil || *run.Summary != "summary" {
		t.Errorf("summary = %v", run.Summary)
	}
}

func TestNullAgentStep(t *testing.T) {
	now := time.Now()
	step := NullAgentStep(
		sql.NullString{String: "step-1", Valid: true},
		sql.NullString{String: "run-1", Valid: true},
		sql.NullInt32{Int32: 1, Valid: true},
		sql.NullString{String: AgentStepTypeToolCall, Valid: true},
		sql.NullString{String: AgentStepStatusCompleted, Valid: true},
		sql.NullString{String: "content", Valid: true},
		sql.NullString{String: "tool", Valid: true},
		sql.NullString{String: `{"input":true}`, Valid: true},
		sql.NullString{String: `{"output":true}`, Valid: true},
		sql.NullString{String: "cmd", Valid: true},
		sql.NullString{String: "output", Valid: true},
		sql.NullInt32{Int32: 0, Valid: true},
		sql.NullString{String: "main.go", Valid: true},
		sql.NullString{String: "diff", Valid: true},
		sql.NullFloat64{Float64: 0.01, Valid: true},
		sql.NullInt32{Int32: 100, Valid: true},
		sql.NullTime{Time: now, Valid: true},
	)

	if step.ID != "step-1" {
		t.Errorf("id = %q", step.ID)
	}
	if step.StepNumber != 1 {
		t.Errorf("step_number = %d", step.StepNumber)
	}
	if step.ExitCode == nil || *step.ExitCode != 0 {
		t.Errorf("exit_code = %v", step.ExitCode)
	}
	if step.Cost != 0.01 {
		t.Errorf("cost = %f", step.Cost)
	}
	if step.LatencyMs != 100 {
		t.Errorf("latency_ms = %d", step.LatencyMs)
	}
}
