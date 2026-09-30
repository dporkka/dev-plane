package readiness

import "testing"

func TestAssessTaskReadyWithBoundedVerifiedSpec(t *testing.T) {
	report := AssessTask(TaskAssessmentInput{
		HasSpec:            true,
		ImplementationPlan: []string{"edit handler", "add test"},
		FilesToChange:      []string{"apps/api/handler.go"},
		AcceptanceCriteria: []string{"request returns 200"},
		TestPlan:           "go test ./...",
		RiskLevel:          "low",
	})
	if report.Status != StatusReady {
		t.Fatalf("status = %q, want %q", report.Status, StatusReady)
	}
}

func TestAssessTaskBlocksMissingSpec(t *testing.T) {
	report := AssessTask(TaskAssessmentInput{})
	if report.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", report.Status, StatusBlocked)
	}
	if got := findCheck(t, report, "spec").Status; got != StatusBlocked {
		t.Fatalf("spec status = %q, want %q", got, StatusBlocked)
	}
}

func TestAssessTaskBlocksMissingAcceptanceCriteria(t *testing.T) {
	report := AssessTask(TaskAssessmentInput{
		HasSpec:       true,
		FilesToChange: []string{"file.go"},
		TestPlan:      "go test ./...",
	})
	if got := findCheck(t, report, "acceptance").Status; got != StatusBlocked {
		t.Fatalf("acceptance status = %q, want %q", got, StatusBlocked)
	}
}

func TestAssessTaskBlocksWithoutTestPlanOrDetectedTestCommand(t *testing.T) {
	report := AssessTask(TaskAssessmentInput{
		HasSpec:            true,
		FilesToChange:      []string{"file.go"},
		AcceptanceCriteria: []string{"works"},
	})
	if got := findCheck(t, report, "verification").Status; got != StatusBlocked {
		t.Fatalf("verification status = %q, want %q", got, StatusBlocked)
	}
}

func TestAssessTaskUsesDetectedTestCommand(t *testing.T) {
	report := AssessTask(TaskAssessmentInput{
		HasSpec:            true,
		ImplementationPlan: []string{"change file"},
		FilesToChange:      []string{"file.go"},
		AcceptanceCriteria: []string{"works"},
		TestCommand:        "go test ./...",
		RiskLevel:          "low",
	})
	if report.Status != StatusReady {
		t.Fatalf("status = %q, want %q", report.Status, StatusReady)
	}
}

func TestAssessTaskMarksUnboundedScopeAsAttention(t *testing.T) {
	report := AssessTask(TaskAssessmentInput{
		HasSpec:            true,
		ImplementationPlan: []string{"refactor"},
		AcceptanceCriteria: []string{"tests pass"},
		TestPlan:           "go test ./...",
		RiskLevel:          "low",
	})
	if report.Status != StatusAttention {
		t.Fatalf("status = %q, want %q", report.Status, StatusAttention)
	}
	if got := findCheck(t, report, "scope").Status; got != StatusAttention {
		t.Fatalf("scope status = %q, want %q", got, StatusAttention)
	}
}

func TestAssessTaskBlocksCriticalRiskWithoutApproval(t *testing.T) {
	report := AssessTask(TaskAssessmentInput{
		HasSpec:            true,
		ImplementationPlan: []string{"change auth"},
		FilesToChange:      []string{"auth.go"},
		AcceptanceCriteria: []string{"auth remains enforced"},
		TestPlan:           "go test ./...",
		RiskLevel:          "critical",
		RollbackPlan:       "revert commit",
	})
	if got := findCheck(t, report, "risk_controls").Status; got != StatusBlocked {
		t.Fatalf("risk_controls status = %q, want %q", got, StatusBlocked)
	}
}
