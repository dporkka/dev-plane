package repoprotocol

import (
	"strings"
	"testing"
	"time"
)

func TestWorkItemClaimPreventsDoubleClaimAndAllowsExpiredReclaim(t *testing.T) {
	now := time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)
	item := WorkItem{
		ID:             "DEV-201",
		Repository:     "dporkka/dev-plane",
		Objective:      "Schedule durable work",
		OwnershipPaths: []string{"packages/scheduler/**"},
		Risk:           RiskMedium,
		Cost:           2,
		BaseSHA:        "abc123",
		State:          WorkReady,
	}

	if err := item.Claim("agent-a", now, 10*time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if item.State != WorkClaimed || item.ClaimedBy != "agent-a" || item.LeaseUntil == nil {
		t.Fatalf("Claim() did not persist claim state: %#v", item)
	}
	if !item.HasActiveLease(now.Add(5 * time.Minute)) {
		t.Fatal("HasActiveLease() = false during active lease")
	}
	if err := item.Claim("agent-b", now.Add(5*time.Minute), 10*time.Minute); err == nil || !strings.Contains(err.Error(), "active lease") {
		t.Fatalf("second Claim() error = %v, want active lease error", err)
	}

	if err := item.Claim("agent-b", now.Add(11*time.Minute), 10*time.Minute); err != nil {
		t.Fatalf("expired Claim() error = %v", err)
	}
	if item.ClaimedBy != "agent-b" {
		t.Fatalf("ClaimedBy = %q, want agent-b", item.ClaimedBy)
	}
}

func TestWorkItemValidateRequiresLeaseForClaimedState(t *testing.T) {
	item := WorkItem{
		ID:             "DEV-202",
		Repository:     "dporkka/dev-plane",
		Objective:      "Invalid claimed item",
		OwnershipPaths: []string{"packages/scheduler/**"},
		Risk:           RiskLow,
		Cost:           1,
		BaseSHA:        "abc123",
		State:          WorkClaimed,
		ClaimedBy:      "agent-a",
	}

	err := item.Validate()
	if err == nil || !strings.Contains(err.Error(), "lease_until") {
		t.Fatalf("Validate() error = %v, want lease_until error", err)
	}
}
