package budget

import (
	"context"
	"testing"
)

func TestCheckRunDelegatesDollarLimitsToExternalSpendAuthority(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	engine := NewEngine(db)
	budget := makeBudget()
	seedPassingDBData(t, db)

	// Deliberately exceed the local max cost. When the model gateway is the
	// authoritative spend enforcer, Dev Plane must not pretend this local
	// estimate is authoritative. Non-dollar limits still remain in force.
	state := &RunState{
		CostSoFar:             99,
		ExternalCostAuthority: true,
		DurationMinutes:       15,
		ModelCalls:            10,
		ToolCalls:             5,
		ShellCommands:         2,
	}

	result, err := engine.CheckRun(context.Background(), budget, state)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("expected gateway-authoritative spend to skip local dollar limits, got %s", result.Reason)
	}
	if result.CostAuthority != CostAuthorityExternal {
		t.Fatalf("cost authority = %q, want %q", result.CostAuthority, CostAuthorityExternal)
	}
	if result.Remaining != -1 {
		t.Fatalf("remaining = %.4f, want -1 when external gateway owns spend", result.Remaining)
	}
}

func TestCheckRunExternalSpendAuthorityStillEnforcesOperationalLimits(t *testing.T) {
	engine := NewEngine(nil)
	budget := makeBudget()
	state := &RunState{
		ExternalCostAuthority: true,
		ModelCalls:            budget.MaxModelCalls + 1,
	}

	result, err := engine.CheckRun(context.Background(), budget, state)
	if err != nil {
		t.Fatalf("CheckRun() error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected model-call limit to remain enforced")
	}
}
