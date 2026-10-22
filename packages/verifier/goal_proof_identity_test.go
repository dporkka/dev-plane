package verifier

import (
	"testing"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

func TestGoalProofFromEvidenceBundleObservationTimeVersionsIdenticalContent(t *testing.T) {
	goal := proofTestGoal()
	bundle := repoprotocol.EvidenceBundle{
		WorkItemID: "DEV-506",
		BaseSHA:    "base123",
		HeadSHA:    "head456",
		Gates: []repoprotocol.GateEvidence{
			{Name: "test", Status: repoprotocol.GatePassed, Output: "passed"},
		},
	}
	firstAt := time.Date(2026, 10, 4, 17, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(time.Minute)

	first, err := GoalProofFromEvidenceBundle(goal, "tests", bundle, firstAt)
	if err != nil {
		t.Fatalf("first GoalProofFromEvidenceBundle() error = %v", err)
	}
	second, err := GoalProofFromEvidenceBundle(goal, "tests", bundle, secondAt)
	if err != nil {
		t.Fatalf("second GoalProofFromEvidenceBundle() error = %v", err)
	}
	if first.EvidenceID == second.EvidenceID {
		t.Fatalf("different evidence observations reused identity %q", first.EvidenceID)
	}
}
