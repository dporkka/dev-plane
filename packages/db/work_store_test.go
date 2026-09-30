package db

import (
	"context"
	"reflect"
	"testing"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

func TestWorkItemStoreRoundTrip(t *testing.T) {
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	now := time.Date(2026, 9, 30, 15, 30, 0, 0, time.UTC)
	lease := now.Add(15 * time.Minute)
	item := repoprotocol.WorkItem{
		ID:                 "DEV-301",
		Repository:         "dporkka/dev-plane",
		Objective:          "Persist durable work",
		AcceptanceCriteria: []string{"survives restart", "round trips exactly"},
		ParentID:           "DEV-300",
		DiscoveredFromID:   "DEV-299",
		DependsOn:          []string{"DEV-298"},
		OwnershipPaths:     []string{"packages/db/**"},
		Risk:               repoprotocol.RiskHigh,
		Cost:               2,
		RequiredGates:      []string{"changed", repoprotocol.GateIndependentReview},
		BaseSHA:            "abc123",
		ClaimedBy:          "agent-a",
		LeaseUntil:         &lease,
		State:              repoprotocol.WorkClaimed,
	}

	if err := database.PutWorkItem(context.Background(), item); err != nil {
		t.Fatalf("PutWorkItem() error = %v", err)
	}
	got, err := database.GetWorkItem(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetWorkItem() error = %v", err)
	}

	if !reflect.DeepEqual(got, item) {
		t.Fatalf("GetWorkItem() = %#v, want %#v", got, item)
	}
}

func TestWorkItemStoreUpsertReplacesClaimState(t *testing.T) {
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	item := repoprotocol.WorkItem{
		ID:             "DEV-302",
		Repository:     "dporkka/dev-plane",
		Objective:      "Update durable work",
		OwnershipPaths: []string{"packages/db/**"},
		Risk:           repoprotocol.RiskLow,
		Cost:           1,
		BaseSHA:        "abc123",
		State:          repoprotocol.WorkReady,
	}
	if err := database.PutWorkItem(context.Background(), item); err != nil {
		t.Fatalf("PutWorkItem(initial) error = %v", err)
	}

	now := time.Date(2026, 9, 30, 15, 30, 0, 0, time.UTC)
	if err := item.Claim("agent-b", now, 10*time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := database.PutWorkItem(context.Background(), item); err != nil {
		t.Fatalf("PutWorkItem(updated) error = %v", err)
	}

	got, err := database.GetWorkItem(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetWorkItem() error = %v", err)
	}
	if got.State != repoprotocol.WorkClaimed || got.ClaimedBy != "agent-b" || got.LeaseUntil == nil {
		t.Fatalf("updated work item = %#v", got)
	}
}

func TestEvidenceStoreRoundTripAndExactHeadLookup(t *testing.T) {
	database, err := New(":memory:")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	bundle := repoprotocol.EvidenceBundle{
		WorkItemID: "DEV-303",
		BaseSHA:    "base123",
		HeadSHA:    "head456",
		Gates: []repoprotocol.GateEvidence{
			{Name: "changed", Status: repoprotocol.GatePassed, Command: "make verify-changed"},
			{Name: repoprotocol.GateIndependentReview, Status: repoprotocol.GatePassed},
		},
	}
	if err := database.PutEvidenceBundle(context.Background(), bundle); err != nil {
		t.Fatalf("PutEvidenceBundle() error = %v", err)
	}

	got, err := database.GetEvidenceBundle(context.Background(), bundle.WorkItemID, bundle.HeadSHA)
	if err != nil {
		t.Fatalf("GetEvidenceBundle() error = %v", err)
	}
	if !reflect.DeepEqual(got, bundle) {
		t.Fatalf("GetEvidenceBundle() = %#v, want %#v", got, bundle)
	}
	if err := got.ValidateHead("head456"); err != nil {
		t.Fatalf("ValidateHead() error = %v", err)
	}
}
