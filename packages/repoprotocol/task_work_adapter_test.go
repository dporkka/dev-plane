package repoprotocol

import (
	"reflect"
	"strings"
	"testing"
)

func validTaskWorkInput() TaskWorkInput {
	return TaskWorkInput{
		TaskID:             "task-141",
		Repository:         "dporkka/dev-plane",
		Objective:          "Require exact-head verification",
		AcceptanceCriteria: []string{"Passing exact-head evidence", "Review tied to head SHA"},
		FilesToChange:      []string{"./apps/worker/internal/handlers/run_handlers.go", "packages/verifier/**"},
		FilesToCreate:      []string{"apps/worker/internal/handlers/run_verification_test.go", "packages/verifier/**"},
		Risk:               RiskHigh,
		ParallelCost:       2,
		RequiredGates:      []string{"fast", GateIndependentReview, "fast"},
		BaseSHA:            strings.Repeat("a", 40),
		DependsOn:          []string{"task-140", "task-140"},
	}
}

func TestBuildWorkItemMapsExplicitTaskPolicyWithoutInventingCost(t *testing.T) {
	input := validTaskWorkInput()
	item, err := BuildWorkItem(input)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != input.TaskID || item.BaseSHA != input.BaseSHA ||
		item.Repository != input.Repository || item.Objective != input.Objective ||
		item.Risk != RiskHigh || item.Cost != 2 || item.State != WorkImplementing {
		t.Fatalf("incorrect durable task mapping: %+v", item)
	}
	if !reflect.DeepEqual(item.OwnershipPaths, []string{
		"apps/worker/internal/handlers/run_handlers.go",
		"packages/verifier/**",
		"apps/worker/internal/handlers/run_verification_test.go",
	}) {
		t.Fatalf("ownership paths must be normalized and deduplicated: %v", item.OwnershipPaths)
	}
	if !reflect.DeepEqual(item.RequiredGates, []string{"fast", GateIndependentReview}) {
		t.Fatalf("explicit gates not preserved: %v", item.RequiredGates)
	}
	if !reflect.DeepEqual(item.DependsOn, []string{"task-140"}) {
		t.Fatalf("dependencies not normalized: %v", item.DependsOn)
	}
	if err := item.Validate(); err != nil {
		t.Fatalf("generated WorkItem must validate: %v", err)
	}
}

func TestBuildWorkItemFailsClosedForMissingOrUnsafePolicy(t *testing.T) {
	cases := []struct {
		name string
		edit func(*TaskWorkInput)
	}{
		{"missing task", func(in *TaskWorkInput) { in.TaskID = "" }},
		{"missing repository", func(in *TaskWorkInput) { in.Repository = "" }},
		{"missing objective", func(in *TaskWorkInput) { in.Objective = "" }},
		{"missing acceptance", func(in *TaskWorkInput) { in.AcceptanceCriteria = nil }},
		{"missing paths", func(in *TaskWorkInput) { in.FilesToChange = nil; in.FilesToCreate = nil }},
		{"traversal", func(in *TaskWorkInput) { in.FilesToChange = []string{"../outside.go"} }},
		{"missing risk", func(in *TaskWorkInput) { in.Risk = "" }},
		{"missing parallel cost", func(in *TaskWorkInput) { in.ParallelCost = 0 }},
		{"negative parallel cost", func(in *TaskWorkInput) { in.ParallelCost = -1 }},
		{"missing required gates", func(in *TaskWorkInput) { in.RequiredGates = nil }},
		{"empty gate", func(in *TaskWorkInput) { in.RequiredGates = []string{" "} }},
		{"missing immutable SHA", func(in *TaskWorkInput) { in.BaseSHA = "" }},
		{"branch name instead of SHA", func(in *TaskWorkInput) { in.BaseSHA = "main" }},
		{"invalid SHA characters", func(in *TaskWorkInput) { in.BaseSHA = strings.Repeat("g", 40) }},
		{"self dependency", func(in *TaskWorkInput) { in.DependsOn = []string{in.TaskID} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validTaskWorkInput()
			tc.edit(&in)
			if _, err := BuildWorkItem(in); err == nil {
				t.Fatalf("expected fail-closed rejection for %s", tc.name)
			}
		})
	}
}

func TestBuildWorkItemDoesNotMutateInputSlices(t *testing.T) {
	in := validTaskWorkInput()
	original := append([]string(nil), in.FilesToChange...)
	item, err := BuildWorkItem(in)
	if err != nil {
		t.Fatal(err)
	}
	item.OwnershipPaths[0] = "modified.go"
	if !reflect.DeepEqual(in.FilesToChange, original) {
		t.Fatalf("input modified by mapping: %+v", in.FilesToChange)
	}
}
