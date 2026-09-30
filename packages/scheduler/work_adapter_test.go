package scheduler

import (
	"reflect"
	"strings"
	"testing"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

func workItem(id string, owns []string, cost int) repoprotocol.WorkItem {
	return repoprotocol.WorkItem{
		ID:             id,
		Repository:     "dporkka/dev-plane",
		Objective:      "test " + id,
		OwnershipPaths: owns,
		Risk:           repoprotocol.RiskMedium,
		Cost:           cost,
		BaseSHA:        "abc123",
		State:          repoprotocol.WorkReady,
	}
}

func TestNextWorkReusesSchedulerForCostBudgetAndOwnership(t *testing.T) {
	items := []repoprotocol.WorkItem{
		workItem("A", []string{"apps/api"}, 2),
		workItem("B", []string{"apps/api/routes"}, 2),
		workItem("C", []string{"apps/web"}, 2),
	}

	decision, err := NextWork(items, WorkScheduleOptions{
		MaxParallelCost: 4,
		Now:             time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NextWork() error = %v", err)
	}

	want := []string{"A", "C"}
	if !reflect.DeepEqual(decision.Ready, want) {
		t.Fatalf("NextWork().Ready = %#v, want %#v", decision.Ready, want)
	}
}

func TestNextWorkTreatsActiveClaimAsRunningAndRespectsDependencies(t *testing.T) {
	now := time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)
	a := workItem("A", []string{"apps/api"}, 3)
	if err := a.Claim("agent-a", now, 10*time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	b := workItem("B", []string{"apps/web"}, 1)
	b.DependsOn = []string{"A"}
	c := workItem("C", []string{"packages/shared"}, 2)
	d := workItem("D", []string{"docs"}, 1)

	decision, err := NextWork([]repoprotocol.WorkItem{a, b, c, d}, WorkScheduleOptions{
		MaxParallelCost: 4,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("NextWork() error = %v", err)
	}

	if !reflect.DeepEqual(decision.Ready, []string{"D"}) {
		t.Fatalf("NextWork().Ready = %#v, want [D]", decision.Ready)
	}
}

func TestNextWorkReclaimsExpiredClaim(t *testing.T) {
	now := time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)
	item := workItem("A", []string{"apps/api"}, 1)
	if err := item.Claim("agent-a", now.Add(-20*time.Minute), 10*time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	decision, err := NextWork([]repoprotocol.WorkItem{item}, WorkScheduleOptions{
		MaxParallelCost: 1,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("NextWork() error = %v", err)
	}
	if !reflect.DeepEqual(decision.Ready, []string{"A"}) {
		t.Fatalf("NextWork().Ready = %#v, want [A]", decision.Ready)
	}
}

func TestNextWorkRejectsDraftWork(t *testing.T) {
	item := workItem("A", []string{"apps/api"}, 1)
	item.State = repoprotocol.WorkDraft

	_, err := NextWork([]repoprotocol.WorkItem{item}, WorkScheduleOptions{
		MaxParallelCost: 1,
		Now:             time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "draft work") {
		t.Fatalf("NextWork() error = %v, want draft work error", err)
	}
}
