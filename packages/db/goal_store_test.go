package db

import (
	"context"
	"reflect"
	"testing"

	models "github.com/ai-dev-control-plane/models"
	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

func TestGoalStoreAssignsEpochAndRoundTrips(t *testing.T) {
	database := newGoalTestDB(t)
	ctx := context.Background()

	goal := models.Goal{
		ID:             "goal-1",
		OrganizationID: "org-1",
		CreatedBy:      "user-1",
		Title:          "Ship demo-ready Adacavo",
		Objective:      "Make the demo path reliable end-to-end",
		Status:         models.GoalStatusActive,
		SuccessCriteria: []models.GoalCriterion{
			{ID: "golden-path", Description: "Golden path passes", Required: true},
		},
	}

	stored, err := database.PutGoal(ctx, goal)
	if err != nil {
		t.Fatalf("PutGoal() error = %v", err)
	}
	if stored.ProofEpoch == "" {
		t.Fatal("PutGoal() proof_epoch is empty")
	}

	got, err := database.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal() error = %v", err)
	}
	if !reflect.DeepEqual(got, stored) {
		t.Fatalf("GetGoal() = %#v, want %#v", got, stored)
	}
}

func TestPutGoalRotatesOnlyWhenProofScopeChanges(t *testing.T) {
	database := newGoalTestDB(t)
	ctx := context.Background()

	goal := models.Goal{
		ID:             "goal-2",
		OrganizationID: "org-1",
		CreatedBy:      "user-1",
		Title:          "Ship",
		Objective:      "Ship safely",
		Status:         models.GoalStatusActive,
		SuccessCriteria: []models.GoalCriterion{
			{ID: "tests", Description: "Tests pass", Required: true},
		},
	}

	stored, err := database.PutGoal(ctx, goal)
	if err != nil {
		t.Fatalf("PutGoal(initial) error = %v", err)
	}
	initialEpoch := stored.ProofEpoch

	stored.Status = models.GoalStatusVerifying
	operational, err := database.PutGoal(ctx, stored)
	if err != nil {
		t.Fatalf("PutGoal(operational) error = %v", err)
	}
	if operational.ProofEpoch != initialEpoch {
		t.Fatalf("operational update rotated epoch: got %q want %q", operational.ProofEpoch, initialEpoch)
	}

	operational.Objective = "Ship safely with zero browser errors"
	scopeChanged, err := database.PutGoal(ctx, operational)
	if err != nil {
		t.Fatalf("PutGoal(scope changed) error = %v", err)
	}
	if scopeChanged.ProofEpoch == initialEpoch {
		t.Fatalf("proof-scope update did not rotate epoch %q", initialEpoch)
	}

	previousEpoch := scopeChanged.ProofEpoch
	scopeChanged.SuccessCriteria[0].Description = "Full test suite passes"
	criteriaChanged, err := database.PutGoal(ctx, scopeChanged)
	if err != nil {
		t.Fatalf("PutGoal(criteria changed) error = %v", err)
	}
	if criteriaChanged.ProofEpoch == previousEpoch {
		t.Fatalf("criterion update did not rotate epoch %q", previousEpoch)
	}
}

func TestLinkGoalWorkItemRotatesEpochOnlyForNewSubjectRevision(t *testing.T) {
	database := newGoalTestDB(t)
	ctx := context.Background()

	goal := models.Goal{
		ID:             "goal-3",
		OrganizationID: "org-1",
		CreatedBy:      "user-1",
		Title:          "Ship",
		Objective:      "Ship safely",
		Status:         models.GoalStatusActive,
		SuccessCriteria: []models.GoalCriterion{
			{ID: "tests", Description: "Tests pass", Required: true},
		},
	}
	stored, err := database.PutGoal(ctx, goal)
	if err != nil {
		t.Fatalf("PutGoal() error = %v", err)
	}
	initialEpoch := stored.ProofEpoch

	item := repoprotocol.WorkItem{
		ID:             "DEV-401",
		Repository:     "dporkka/dev-plane",
		Objective:      "Implement the goal slice",
		OwnershipPaths: []string{"packages/db/**"},
		Risk:           repoprotocol.RiskMedium,
		Cost:           1,
		BaseSHA:        "base123",
		State:          repoprotocol.WorkReady,
	}
	if err := database.PutWorkItem(ctx, item); err != nil {
		t.Fatalf("PutWorkItem() error = %v", err)
	}

	linked, err := database.LinkGoalWorkItem(ctx, goal.ID, item.ID, "git-commit:abc")
	if err != nil {
		t.Fatalf("LinkGoalWorkItem(first) error = %v", err)
	}
	if linked.ProofEpoch == initialEpoch {
		t.Fatalf("first link did not rotate epoch %q", initialEpoch)
	}
	linkedEpoch := linked.ProofEpoch

	replayed, err := database.LinkGoalWorkItem(ctx, goal.ID, item.ID, "git-commit:abc")
	if err != nil {
		t.Fatalf("LinkGoalWorkItem(replay) error = %v", err)
	}
	if replayed.ProofEpoch != linkedEpoch {
		t.Fatalf("idempotent replay rotated epoch: got %q want %q", replayed.ProofEpoch, linkedEpoch)
	}

	advanced, err := database.LinkGoalWorkItem(ctx, goal.ID, item.ID, "git-commit:def")
	if err != nil {
		t.Fatalf("LinkGoalWorkItem(advance) error = %v", err)
	}
	if advanced.ProofEpoch == linkedEpoch {
		t.Fatalf("new subject revision did not rotate epoch %q", linkedEpoch)
	}

	links, err := database.ListGoalWorkItems(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListGoalWorkItems() error = %v", err)
	}
	want := []GoalWorkItemLink{{GoalID: goal.ID, WorkItemID: item.ID, SubjectRevision: "git-commit:def"}}
	if !reflect.DeepEqual(links, want) {
		t.Fatalf("ListGoalWorkItems() = %#v, want %#v", links, want)
	}
}

func TestLinkGoalWorkItemRejectsEmptyRevision(t *testing.T) {
	database := newGoalTestDB(t)
	ctx := context.Background()

	goal := models.Goal{
		ID:             "goal-4",
		OrganizationID: "org-1",
		CreatedBy:      "user-1",
		Title:          "Ship",
		Objective:      "Ship safely",
		Status:         models.GoalStatusActive,
		SuccessCriteria: []models.GoalCriterion{
			{ID: "tests", Description: "Tests pass", Required: true},
		},
	}
	if _, err := database.PutGoal(ctx, goal); err != nil {
		t.Fatalf("PutGoal() error = %v", err)
	}

	item := repoprotocol.WorkItem{
		ID:             "DEV-402",
		Repository:     "dporkka/dev-plane",
		Objective:      "Implement the goal slice",
		OwnershipPaths: []string{"packages/db/**"},
		Risk:           repoprotocol.RiskLow,
		Cost:           1,
		BaseSHA:        "base123",
		State:          repoprotocol.WorkReady,
	}
	if err := database.PutWorkItem(ctx, item); err != nil {
		t.Fatalf("PutWorkItem() error = %v", err)
	}

	if _, err := database.LinkGoalWorkItem(ctx, goal.ID, item.ID, ""); err == nil {
		t.Fatal("LinkGoalWorkItem() error = nil, want empty revision rejection")
	}
}

func newGoalTestDB(t *testing.T) *DB {
	t.Helper()
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	if _, err := database.Exec(`INSERT INTO organizations (id, name, slug) VALUES (?, ?, ?)`, "org-1", "Org One", "org-one"); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO users (id, organization_id, email, role) VALUES (?, ?, ?, ?)`, "user-1", "org-1", "user@example.com", "admin"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return database
}
