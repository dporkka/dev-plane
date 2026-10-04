package verifier

import (
	"strings"
	"testing"
	"time"

	models "github.com/ai-dev-control-plane/models"
	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

func TestGoalProofFromEvidenceBundlePassed(t *testing.T) {
	goal := proofTestGoal()
	observedAt := time.Date(2026, 10, 4, 16, 0, 0, 0, time.UTC)
	bundle := repoprotocol.EvidenceBundle{
		WorkItemID: "DEV-501",
		BaseSHA:    "base123",
		HeadSHA:    "head456",
		Gates: []repoprotocol.GateEvidence{
			{Name: "static", Status: repoprotocol.GatePassed},
			{Name: "test", Status: repoprotocol.GatePassed},
		},
	}

	proof, err := GoalProofFromEvidenceBundle(goal, "tests", bundle, observedAt)
	if err != nil {
		t.Fatalf("GoalProofFromEvidenceBundle() error = %v", err)
	}
	if proof.Status != models.GoalProofPassed {
		t.Fatalf("status = %q, want %q", proof.Status, models.GoalProofPassed)
	}
	if !strings.HasPrefix(proof.EvidenceID, "evidence-bundle:DEV-501:head456:sha256:") {
		t.Fatalf("evidence_id = %q, want content-bound evidence bundle id", proof.EvidenceID)
	}
	if proof.SubjectRevision != "git-commit:head456" {
		t.Fatalf("subject_revision = %q", proof.SubjectRevision)
	}
	if proof.ProofEpoch != goal.ProofEpoch {
		t.Fatalf("proof_epoch = %q, want %q", proof.ProofEpoch, goal.ProofEpoch)
	}
	if proof.CriterionDigest != goal.SuccessCriteria[0].Digest() {
		t.Fatalf("criterion_digest = %q, want %q", proof.CriterionDigest, goal.SuccessCriteria[0].Digest())
	}
}

func TestGoalProofFromEvidenceBundleFailedGate(t *testing.T) {
	goal := proofTestGoal()
	bundle := repoprotocol.EvidenceBundle{
		WorkItemID: "DEV-502",
		BaseSHA:    "base123",
		HeadSHA:    "head456",
		Gates: []repoprotocol.GateEvidence{
			{Name: "static", Status: repoprotocol.GatePassed},
			{Name: "test", Status: repoprotocol.GateFailed},
		},
	}

	proof, err := GoalProofFromEvidenceBundle(goal, "tests", bundle, time.Now().UTC())
	if err != nil {
		t.Fatalf("GoalProofFromEvidenceBundle() error = %v", err)
	}
	if proof.Status != models.GoalProofFailed {
		t.Fatalf("status = %q, want %q", proof.Status, models.GoalProofFailed)
	}
}

func TestGoalProofFromEvidenceBundleRerunGetsNewImmutableIdentity(t *testing.T) {
	goal := proofTestGoal()
	at := time.Date(2026, 10, 4, 16, 30, 0, 0, time.UTC)
	failed := repoprotocol.EvidenceBundle{
		WorkItemID: "DEV-505",
		BaseSHA:    "base123",
		HeadSHA:    "head456",
		Gates:      []repoprotocol.GateEvidence{{Name: "test", Status: repoprotocol.GateFailed, Output: "attempt 1 failed"}},
	}
	passed := failed
	passed.Gates = []repoprotocol.GateEvidence{{Name: "test", Status: repoprotocol.GatePassed, Output: "attempt 2 passed"}}

	failedProof, err := GoalProofFromEvidenceBundle(goal, "tests", failed, at)
	if err != nil {
		t.Fatalf("failed GoalProofFromEvidenceBundle() error = %v", err)
	}
	passedProof, err := GoalProofFromEvidenceBundle(goal, "tests", passed, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("passed GoalProofFromEvidenceBundle() error = %v", err)
	}
	if failedProof.EvidenceID == passedProof.EvidenceID {
		t.Fatalf("rerun reused evidence identity %q", failedProof.EvidenceID)
	}
	if failedProof.SubjectRevision != passedProof.SubjectRevision {
		t.Fatalf("same-head rerun subject revisions differ: %q vs %q", failedProof.SubjectRevision, passedProof.SubjectRevision)
	}
}

func TestGoalProofFromEvidenceBundleRejectsIncompleteEvidence(t *testing.T) {
	goal := proofTestGoal()
	for _, status := range []repoprotocol.GateStatus{repoprotocol.GatePending, repoprotocol.GateSkipped, "unknown"} {
		bundle := repoprotocol.EvidenceBundle{
			WorkItemID: "DEV-503",
			BaseSHA:    "base123",
			HeadSHA:    "head456",
			Gates:      []repoprotocol.GateEvidence{{Name: "test", Status: status}},
		}
		if _, err := GoalProofFromEvidenceBundle(goal, "tests", bundle, time.Now().UTC()); err == nil {
			t.Fatalf("status %q: error = nil, want incomplete evidence rejection", status)
		}
	}
}

func TestGoalProofFromEvidenceBundleRejectsMalformedAuthority(t *testing.T) {
	goal := proofTestGoal()
	valid := repoprotocol.EvidenceBundle{
		WorkItemID: "DEV-504",
		BaseSHA:    "base123",
		HeadSHA:    "head456",
		Gates:      []repoprotocol.GateEvidence{{Name: "test", Status: repoprotocol.GatePassed}},
	}

	cases := []struct {
		name      string
		goal      models.Goal
		criterion string
		bundle    repoprotocol.EvidenceBundle
		observed  time.Time
	}{
		{name: "missing epoch", goal: func() models.Goal { g := goal; g.ProofEpoch = ""; return g }(), criterion: "tests", bundle: valid, observed: time.Now().UTC()},
		{name: "unknown criterion", goal: goal, criterion: "security", bundle: valid, observed: time.Now().UTC()},
		{name: "missing work item", goal: goal, criterion: "tests", bundle: func() repoprotocol.EvidenceBundle { b := valid; b.WorkItemID = ""; return b }(), observed: time.Now().UTC()},
		{name: "missing base", goal: goal, criterion: "tests", bundle: func() repoprotocol.EvidenceBundle { b := valid; b.BaseSHA = ""; return b }(), observed: time.Now().UTC()},
		{name: "missing head", goal: goal, criterion: "tests", bundle: func() repoprotocol.EvidenceBundle { b := valid; b.HeadSHA = ""; return b }(), observed: time.Now().UTC()},
		{name: "missing gates", goal: goal, criterion: "tests", bundle: func() repoprotocol.EvidenceBundle { b := valid; b.Gates = nil; return b }(), observed: time.Now().UTC()},
		{name: "zero observed at", goal: goal, criterion: "tests", bundle: valid, observed: time.Time{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := GoalProofFromEvidenceBundle(tc.goal, tc.criterion, tc.bundle, tc.observed); err == nil {
				t.Fatal("error = nil, want malformed authority rejection")
			}
		})
	}
}

func proofTestGoal() models.Goal {
	return models.Goal{
		ID:             "goal-proof-adapter",
		OrganizationID: "org-1",
		CreatedBy:      "user-1",
		Title:          "Ship",
		Objective:      "Ship safely",
		Status:         models.GoalStatusVerifying,
		ProofEpoch:     "epoch:current",
		SuccessCriteria: []models.GoalCriterion{
			{ID: "tests", Description: "Verification gates pass", Required: true},
		},
	}
}
