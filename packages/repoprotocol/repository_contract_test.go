package repoprotocol

import (
	"os"
	"reflect"
	"testing"
)

func TestRepositoryContractParsesAndPreservesSafetyDefaults(t *testing.T) {
	raw, err := os.ReadFile("../../devplane.yaml")
	if err != nil {
		t.Fatalf("read repository contract: %v", err)
	}

	cfg, err := ParseConfigYAML(raw)
	if err != nil {
		t.Fatalf("ParseConfigYAML(devplane.yaml) error = %v", err)
	}

	if cfg.Work.Isolation != IsolationWorktree {
		t.Fatalf("work isolation = %q, want %q", cfg.Work.Isolation, IsolationWorktree)
	}
	if !cfg.Work.RequireCleanHandoff {
		t.Fatal("require_clean_handoff = false, want true")
	}
	if !cfg.Review.Independent {
		t.Fatal("review.independent = false, want true")
	}
	if !cfg.Review.ExactHead {
		t.Fatal("review.exact_head = false, want true")
	}

	for _, name := range []string{"fast", "changed", "full", "postgres", "integration", "live"} {
		if _, ok := cfg.Verification[name]; !ok {
			t.Fatalf("verification profile %q is missing", name)
		}
	}

	migrationGates := cfg.RequiredGatesForPaths([]string{"packages/db/migrations/999_example.sql"})
	wantMigrationGates := []string{"full", "postgres", GateIndependentReview, GateHumanApproval}
	if !reflect.DeepEqual(migrationGates, wantMigrationGates) {
		t.Fatalf("migration gates = %#v, want %#v", migrationGates, wantMigrationGates)
	}

	protocolGates := cfg.RequiredGatesForPaths([]string{"packages/repoprotocol/protocol.go"})
	wantProtocolGates := []string{"full", GateIndependentReview, GateHumanApproval}
	if !reflect.DeepEqual(protocolGates, wantProtocolGates) {
		t.Fatalf("repository protocol gates = %#v, want %#v", protocolGates, wantProtocolGates)
	}

	workflowGates := cfg.RequiredGatesForPaths([]string{".github/workflows/ci.yml"})
	wantWorkflowGates := []string{"full", GateIndependentReview, GateHumanApproval}
	if !reflect.DeepEqual(workflowGates, wantWorkflowGates) {
		t.Fatalf("workflow gates = %#v, want %#v", workflowGates, wantWorkflowGates)
	}

	contractGates := cfg.RequiredGatesForPaths([]string{"devplane.yaml", "scripts/verify.sh"})
	wantContractGates := []string{"full", GateIndependentReview, GateHumanApproval}
	if !reflect.DeepEqual(contractGates, wantContractGates) {
		t.Fatalf("contract gates = %#v, want %#v", contractGates, wantContractGates)
	}
}
