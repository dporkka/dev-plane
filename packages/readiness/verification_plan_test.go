package readiness

import (
	"reflect"
	"testing"
)

func TestBuildVerificationPlanIncludesOnlyConfiguredChecksInStableOrder(t *testing.T) {
	plan := BuildVerificationPlan(VerificationCommands{
		Test:      "go test ./...",
		Typecheck: "go build ./...",
		Build:     "go build ./cmd/...",
	})

	want := []VerificationCheck{
		{Name: "tests", Command: "go test ./..."},
		{Name: "typecheck", Command: "go build ./..."},
		{Name: "build", Command: "go build ./cmd/..."},
	}
	if !reflect.DeepEqual(plan.Checks, want) {
		t.Fatalf("checks = %#v, want %#v", plan.Checks, want)
	}
	if !reflect.DeepEqual(plan.RequiredEvidence(), []string{"tests", "typecheck", "build"}) {
		t.Fatalf("required evidence = %#v", plan.RequiredEvidence())
	}
}

func TestBuildVerificationPlanTrimsCommandsAndOmitsEmptyChecks(t *testing.T) {
	plan := BuildVerificationPlan(VerificationCommands{
		Test: "  go test ./...  ",
		Lint: "   ",
	})

	want := []VerificationCheck{{Name: "tests", Command: "go test ./..."}}
	if !reflect.DeepEqual(plan.Checks, want) {
		t.Fatalf("checks = %#v, want %#v", plan.Checks, want)
	}
}

func TestBuildVerificationPlanAllowsManualVerificationWithoutSyntheticMachineChecks(t *testing.T) {
	plan := BuildVerificationPlan(VerificationCommands{})
	if len(plan.Checks) != 0 {
		t.Fatalf("checks = %#v, want none", plan.Checks)
	}
	if len(plan.RequiredEvidence()) != 0 {
		t.Fatalf("required evidence = %#v, want none", plan.RequiredEvidence())
	}
}
