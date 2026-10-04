package models

import (
	"testing"
	"time"
)

func TestGoalValidateRequiresProofableSuccessCriteria(t *testing.T) {
	goal := &Goal{
		ID:             "goal-1",
		OrganizationID: "org-1",
		CreatedBy:      "user-1",
		Title:          "Ship demo-ready Adacavo",
		Objective:      "Make the customer demo path reliable end-to-end",
		Status:         GoalStatusActive,
	}

	if err := goal.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want missing success criteria error")
	}

	goal.SuccessCriteria = []GoalCriterion{{
		ID:          "golden-path",
		Description: "Lead-to-payment golden path passes on a fresh environment",
		Required:    true,
	}}
	if err := goal.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestEvaluateGoalProofRequiresAllRequiredCriteria(t *testing.T) {
	goal := Goal{
		ID:             "goal-1",
		OrganizationID: "org-1",
		CreatedBy:      "user-1",
		Title:          "Ship demo-ready Adacavo",
		Objective:      "Make demos reliable",
		Status:         GoalStatusActive,
		SuccessCriteria: []GoalCriterion{
			{ID: "golden-path", Description: "Golden path passes", Required: true},
			{ID: "browser", Description: "Browser smoke passes", Required: true},
			{ID: "perf", Description: "P95 is below target", Required: false},
		},
	}

	goldenDigest := goal.SuccessCriteria[0].Digest()
	browserDigest := goal.SuccessCriteria[1].Digest()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	evaluation := EvaluateGoalProof(goal, []GoalProof{
		{
			CriterionID:     "golden-path",
			CriterionDigest: goldenDigest,
			EvidenceID:      "evidence-1",
			SubjectRevision: "git-commit:abc",
			Status:          GoalProofPassed,
			ObservedAt:      now,
		},
	})
	if evaluation.Status != GoalEvaluationUnproven {
		t.Fatalf("status = %q, want %q", evaluation.Status, GoalEvaluationUnproven)
	}
	if len(evaluation.MissingRequired) != 1 || evaluation.MissingRequired[0] != "browser" {
		t.Fatalf("missing_required = %#v, want [browser]", evaluation.MissingRequired)
	}

	evaluation = EvaluateGoalProof(goal, []GoalProof{
		{
			CriterionID:     "golden-path",
			CriterionDigest: goldenDigest,
			EvidenceID:      "evidence-1",
			SubjectRevision: "git-commit:abc",
			Status:          GoalProofPassed,
			ObservedAt:      now,
		},
		{
			CriterionID:     "browser",
			CriterionDigest: browserDigest,
			EvidenceID:      "evidence-2",
			SubjectRevision: "git-commit:abc",
			Status:          GoalProofPassed,
			ObservedAt:      now,
		},
	})
	if evaluation.Status != GoalEvaluationProven {
		t.Fatalf("status = %q, want %q", evaluation.Status, GoalEvaluationProven)
	}
}

func TestEvaluateGoalProofFailsClosedOnNewerFailure(t *testing.T) {
	criterion := GoalCriterion{ID: "tests", Description: "Full test suite passes", Required: true}
	goal := Goal{
		ID: "goal-1", OrganizationID: "org-1", CreatedBy: "user-1",
		Title: "Ship", Objective: "Ship safely", Status: GoalStatusActive,
		SuccessCriteria: []GoalCriterion{criterion},
	}
	digest := criterion.Digest()
	old := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)

	evaluation := EvaluateGoalProof(goal, []GoalProof{
		{CriterionID: "tests", CriterionDigest: digest, EvidenceID: "pass", SubjectRevision: "git-commit:abc", Status: GoalProofPassed, ObservedAt: old},
		{CriterionID: "tests", CriterionDigest: digest, EvidenceID: "fail", SubjectRevision: "git-commit:def", Status: GoalProofFailed, ObservedAt: newer},
	})

	if evaluation.Status != GoalEvaluationContradicted {
		t.Fatalf("status = %q, want %q", evaluation.Status, GoalEvaluationContradicted)
	}
	if len(evaluation.FailedRequired) != 1 || evaluation.FailedRequired[0] != "tests" {
		t.Fatalf("failed_required = %#v, want [tests]", evaluation.FailedRequired)
	}
}

func TestEvaluateGoalProofRejectsStaleCriterionEvidence(t *testing.T) {
	criterion := GoalCriterion{ID: "security", Description: "Security review passes", Required: true}
	goal := Goal{
		ID: "goal-1", OrganizationID: "org-1", CreatedBy: "user-1",
		Title: "Ship", Objective: "Ship safely", Status: GoalStatusActive,
		SuccessCriteria: []GoalCriterion{criterion},
	}

	evaluation := EvaluateGoalProof(goal, []GoalProof{
		{
			CriterionID: "security", CriterionDigest: "sha256:old-contract",
			EvidenceID: "evidence-old", SubjectRevision: "git-commit:abc",
			Status: GoalProofPassed, ObservedAt: time.Now(),
		},
	})

	if evaluation.Status != GoalEvaluationUnproven {
		t.Fatalf("status = %q, want %q", evaluation.Status, GoalEvaluationUnproven)
	}
	if len(evaluation.StaleEvidence) != 1 || evaluation.StaleEvidence[0] != "evidence-old" {
		t.Fatalf("stale_evidence = %#v, want [evidence-old]", evaluation.StaleEvidence)
	}
}

func TestEvaluateGoalProofFailsClosedOnConflictingLatestEvidence(t *testing.T) {
	criterion := GoalCriterion{ID: "deploy", Description: "Production smoke passes", Required: true}
	goal := Goal{
		ID: "goal-1", OrganizationID: "org-1", CreatedBy: "user-1",
		Title: "Ship", Objective: "Ship safely", Status: GoalStatusActive,
		SuccessCriteria: []GoalCriterion{criterion},
	}
	digest := criterion.Digest()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	evaluation := EvaluateGoalProof(goal, []GoalProof{
		{CriterionID: "deploy", CriterionDigest: digest, EvidenceID: "pass", SubjectRevision: "deploy:123", Status: GoalProofPassed, ObservedAt: at},
		{CriterionID: "deploy", CriterionDigest: digest, EvidenceID: "fail", SubjectRevision: "deploy:123", Status: GoalProofFailed, ObservedAt: at},
	})

	if evaluation.Status != GoalEvaluationContradicted {
		t.Fatalf("status = %q, want %q", evaluation.Status, GoalEvaluationContradicted)
	}
}
