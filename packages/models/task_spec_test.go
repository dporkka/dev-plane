package models

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func TestNullTaskSpec(t *testing.T) {
	now := time.Now()
	ts := NullTaskSpec(
		sql.NullString{String: "id-1", Valid: true},
		sql.NullString{String: "task-1", Valid: true},
		sql.NullString{String: "summary", Valid: true},
		sql.NullString{String: "problem", Valid: true},
		sql.NullString{String: `["step 1","step 2"]`, Valid: true},
		sql.NullString{String: `["a.go"]`, Valid: true},
		sql.NullString{String: `["b.go"]`, Valid: true},
		sql.NullString{String: `["it works"]`, Valid: true},
		sql.NullString{String: "test plan", Valid: true},
		sql.NullString{String: "low", Valid: true},
		sql.NullString{String: "git revert", Valid: true},
		sql.NullString{String: `["user-1"]`, Valid: true},
		sql.NullFloat64{Float64: 0.5, Valid: true},
		sql.NullString{String: "implementer", Valid: true},
		sql.NullString{String: "gpt-4", Valid: true},
		sql.NullTime{Time: now, Valid: true},
	)

	if ts.ID != "id-1" {
		t.Errorf("id = %q", ts.ID)
	}
	if ts.TaskID != "task-1" {
		t.Errorf("task_id = %q", ts.TaskID)
	}
	if len(ts.ImplementationPlan) != 2 {
		t.Errorf("implementation_plan = %v", ts.ImplementationPlan)
	}
	if len(ts.FilesToChange) != 1 || ts.FilesToChange[0] != "a.go" {
		t.Errorf("files_to_change = %v", ts.FilesToChange)
	}
	if len(ts.FilesToCreate) != 1 || ts.FilesToCreate[0] != "b.go" {
		t.Errorf("files_to_create = %v", ts.FilesToCreate)
	}
	if len(ts.AcceptanceCriteria) != 1 || ts.AcceptanceCriteria[0] != "it works" {
		t.Errorf("acceptance_criteria = %v", ts.AcceptanceCriteria)
	}
	if len(ts.RequiredApprovals) != 1 || ts.RequiredApprovals[0] != "user-1" {
		t.Errorf("required_approvals = %v", ts.RequiredApprovals)
	}
	if ts.EstimatedCost != 0.5 {
		t.Errorf("estimated_cost = %f", ts.EstimatedCost)
	}
	if !ts.GeneratedAt.Equal(now) {
		t.Errorf("generated_at = %v", ts.GeneratedAt)
	}
}

func TestNullProjectConfig(t *testing.T) {
	now := time.Now()
	pc := NullProjectConfig(
		sql.NullString{String: "id-1", Valid: true},
		sql.NullString{String: "repo-1", Valid: true},
		sql.NullString{String: "pnpm", Valid: true},
		sql.NullString{String: "Next.js", Valid: true},
		sql.NullString{String: "jest", Valid: true},
		sql.NullString{String: "eslint .", Valid: true},
		sql.NullString{String: "tsc --noEmit", Valid: true},
		sql.NullString{String: "next dev", Valid: true},
		sql.NullString{String: "next build", Valid: true},
		sql.NullBool{Bool: true, Valid: true},
		sql.NullBool{Bool: false, Valid: true},
		sql.NullTime{Time: now, Valid: true},
		sql.NullTime{Time: now, Valid: true},
	)

	if pc.ID != "id-1" {
		t.Errorf("id = %q", pc.ID)
	}
	if pc.PackageManager != "pnpm" {
		t.Errorf("package_manager = %q", pc.PackageManager)
	}
	if pc.Framework != "Next.js" {
		t.Errorf("framework = %q", pc.Framework)
	}
	if !pc.HasDockerfile || pc.HasDevcontainer {
		t.Errorf("dockerfile=%v devcontainer=%v", pc.HasDockerfile, pc.HasDevcontainer)
	}
}

func TestNullDetectionResult(t *testing.T) {
	now := time.Now()
	dr := NullDetectionResult(
		sql.NullString{String: "id-1", Valid: true},
		sql.NullString{String: "repo-1", Valid: true},
		sql.NullString{String: "ws-1", Valid: true},
		sql.NullString{String: "go", Valid: true},
		sql.NullString{String: "Gin", Valid: true},
		sql.NullString{String: "go test ./...", Valid: true},
		sql.NullString{String: "golangci-lint run", Valid: true},
		sql.NullString{String: "", Valid: false},
		sql.NullString{String: "", Valid: false},
		sql.NullString{String: "go build", Valid: true},
		sql.NullBool{Bool: false, Valid: true},
		sql.NullBool{Bool: false, Valid: true},
		sql.NullString{String: "raw output", Valid: true},
		sql.NullTime{Time: now, Valid: true},
	)

	if dr.ID != "id-1" {
		t.Errorf("id = %q", dr.ID)
	}
	if dr.WorkspaceID == nil || *dr.WorkspaceID != "ws-1" {
		t.Errorf("workspace_id = %v", dr.WorkspaceID)
	}
	if dr.RawOutput != "raw output" {
		t.Errorf("raw_output = %q", dr.RawOutput)
	}
}

func TestTaskSpec_JSONRoundTrip(t *testing.T) {
	ts := &TaskSpec{
		ID:                 "id-1",
		TaskID:             "task-1",
		Summary:            "summary",
		ImplementationPlan: []string{"step 1"},
		EstimatedCost:      0.25,
	}

	data, err := json.Marshal(ts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded TaskSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != ts.ID {
		t.Errorf("id = %q", decoded.ID)
	}
	if len(decoded.ImplementationPlan) != 1 {
		t.Errorf("implementation_plan = %v", decoded.ImplementationPlan)
	}
}
