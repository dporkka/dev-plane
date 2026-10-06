package db

import (
	"context"
	"testing"
	"time"

	models "github.com/ai-dev-control-plane/models"
)

func TestGoalProofStoreReplayUsesSemanticTimestampEquality(t *testing.T) {
	database := newGoalTestDB(t)
	ctx := context.Background()
	goal, err := database.PutGoal(ctx, models.Goal{
		ID:             "goal-proof-time",
		OrganizationID: "org-1",
		CreatedBy:      "user-1",
		Title:          "Ship",
		Objective:      "Ship safely",
		Status:         models.GoalStatusActive,
		SuccessCriteria: []models.GoalCriterion{
			{ID: "tests", Description: "Tests pass", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("PutGoal() error = %v", err)
	}

	instantUTC := time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC)
	instantOffset := instantUTC.In(time.FixedZone("test-offset", 3*60*60))
	proof := models.GoalProof{
		CriterionID:     "tests",
		CriterionDigest: goal.SuccessCriteria[0].Digest(),
		EvidenceID:      "evidence:same-instant",
		SubjectRevision: "git-commit:abc",
		ProofEpoch:      goal.ProofEpoch,
		Status:          models.GoalProofPassed,
		ObservedAt:      instantOffset,
	}
	if err := database.PutGoalProof(ctx, goal.ID, proof); err != nil {
		t.Fatalf("PutGoalProof(first) error = %v", err)
	}

	replay := proof
	replay.ObservedAt = instantUTC
	if err := database.PutGoalProof(ctx, goal.ID, replay); err != nil {
		t.Fatalf("PutGoalProof(same instant replay) error = %v", err)
	}
}
