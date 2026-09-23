package models

import (
	"encoding/json"
	"testing"
)

func TestNLAPExecutionRequestDevPlaneFields(t *testing.T) {
	raw := []byte(`{
		"repository_id":"repo-1",
		"target_branch":"main",
		"goal":{
			"id":"11111111-1111-1111-1111-111111111111",
			"project_id":"nulang-project",
			"intent":"ship feature",
			"status":"running",
			"budget_usd":8
		},
		"task":{
			"id":"22222222-2222-2222-2222-222222222222",
			"goal_id":"11111111-1111-1111-1111-111111111111",
			"manager":"engineering",
			"description":"implement feature",
			"required_capabilities":["code","test"],
			"acceptance_criteria":["tests pass"],
			"budget_usd":4,
			"timeout_secs":901,
			"status":"ready"
		}
	}`)

	var req NLAPExecutionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("validate request: %v", err)
	}

	spec, metadata, acceptance, maxCost, maxRuntime, err := req.DevPlaneFields()
	if err != nil {
		t.Fatalf("convert fields: %v", err)
	}
	if maxCost == nil || *maxCost != 4 {
		t.Fatalf("expected task budget 4, got %v", maxCost)
	}
	if maxRuntime != 16 {
		t.Fatalf("expected 16 minutes from 901 seconds, got %d", maxRuntime)
	}
	if string(acceptance) != `["tests pass"]` {
		t.Fatalf("unexpected acceptance criteria: %s", acceptance)
	}
	if !json.Valid(spec) || !json.Valid(metadata) {
		t.Fatal("spec and metadata must be valid JSON")
	}
}

func TestNLAPTaskAcceptsLegacyTimeout(t *testing.T) {
	var task NLAPTask
	if err := json.Unmarshal([]byte(`{
		"id":"task-1",
		"goal_id":"goal-1",
		"manager":"engineering",
		"description":"work",
		"timeout":120,
		"status":"ready"
	}`), &task); err != nil {
		t.Fatalf("decode legacy task: %v", err)
	}
	if task.TimeoutSecs != 120 {
		t.Fatalf("expected legacy timeout to map to timeout_secs, got %d", task.TimeoutSecs)
	}
}

func TestNLAPExecutionRequestRejectsGoalMismatch(t *testing.T) {
	req := NLAPExecutionRequest{
		RepositoryID: "repo-1",
		Goal: NLAPGoal{ID: "goal-1"},
		Task: NLAPTask{ID: "task-1", GoalID: "goal-2", Description: "work"},
	}
	if err := req.Validate(); err == nil {
		t.Fatal("expected mismatched goal id validation error")
	}
}
