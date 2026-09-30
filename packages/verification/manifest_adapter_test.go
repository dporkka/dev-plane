package verification

import (
	"testing"

	repomanifest "github.com/ai-dev-control-plane/repo-manifest"
)

func TestContractFromManifestUsesFallbackChecksAsRequiredEvidence(t *testing.T) {
	manifest := &repomanifest.Manifest{
		SchemaVersion: 1,
		Commands: repomanifest.Commands{
			Lint:      &repomanifest.Command{Run: "make lint", TimeoutSeconds: 120},
			Typecheck: &repomanifest.Command{Run: "make typecheck", TimeoutSeconds: 180},
			Test:      &repomanifest.Command{Run: "make test", TimeoutSeconds: 300},
			Build:     &repomanifest.Command{Run: "make build", TimeoutSeconds: 600},
			Dev:       &repomanifest.Command{Run: "make dev"},
		},
		Validation: repomanifest.Validation{FallbackChecks: []string{"lint", "typecheck", "test"}},
	}

	contract, err := ContractFromManifest(manifest)
	if err != nil {
		t.Fatalf("ContractFromManifest() error = %v", err)
	}

	if len(contract.Checks) != 4 {
		t.Fatalf("len(Checks) = %d, want 4", len(contract.Checks))
	}

	want := map[string]struct {
		command  string
		required bool
	}{
		"lint":      {"make lint", true},
		"typecheck": {"make typecheck", true},
		"test":      {"make test", true},
		"build":     {"make build", false},
	}
	for _, check := range contract.Checks {
		expected, ok := want[check.ID]
		if !ok {
			t.Fatalf("unexpected check %q", check.ID)
		}
		if check.Command != expected.command || check.Required != expected.required {
			t.Fatalf("check %q = %#v, want command=%q required=%v", check.ID, check, expected.command, expected.required)
		}
	}
}

func TestContractFromManifestRequiresAtLeastOneValidationCommand(t *testing.T) {
	manifest := &repomanifest.Manifest{
		SchemaVersion: 1,
		Commands: repomanifest.Commands{
			Dev: &repomanifest.Command{Run: "make dev"},
		},
	}

	if _, err := ContractFromManifest(manifest); err == nil {
		t.Fatal("ContractFromManifest() error = nil, want validation command error")
	}
}

func TestContractFromManifestDefaultsAllValidationCommandsToRequired(t *testing.T) {
	manifest := &repomanifest.Manifest{
		SchemaVersion: 1,
		Commands: repomanifest.Commands{
			Test:  &repomanifest.Command{Run: "make test"},
			Build: &repomanifest.Command{Run: "make build"},
		},
	}

	contract, err := ContractFromManifest(manifest)
	if err != nil {
		t.Fatalf("ContractFromManifest() error = %v", err)
	}
	for _, check := range contract.Checks {
		if !check.Required {
			t.Fatalf("check %q required = false, want true", check.ID)
		}
	}
}


func TestContractFromPlanBindsAffectedScopeAndRequiresEveryPlannedCheck(t *testing.T) {
	plan := repomanifest.CheckPlan{
		ChangedFiles:       []string{"apps/api/routes.go", "packages/shared/schema.go"},
		ChangedComponents:  []string{"shared"},
		AffectedComponents: []string{"api", "shared"},
		Checks: []repomanifest.PlannedCheck{
			{
				Component: "api",
				Kind:      "test",
				Command:   repomanifest.Command{Run: "go test ./apps/api/...", TimeoutSeconds: 300},
			},
			{
				Component: "shared",
				Kind:      "typecheck",
				Command:   repomanifest.Command{Run: "go test ./packages/shared/...", TimeoutSeconds: 120},
			},
		},
	}

	contract, err := ContractFromPlan(plan)
	if err != nil {
		t.Fatalf("ContractFromPlan() error = %v", err)
	}
	if contract.Scope == nil {
		t.Fatal("ContractFromPlan() Scope = nil")
	}
	if got := contract.Scope.ChangedFiles; len(got) != 2 || got[0] != "apps/api/routes.go" || got[1] != "packages/shared/schema.go" {
		t.Fatalf("Scope.ChangedFiles = %#v", got)
	}
	if got := contract.Scope.AffectedComponents; len(got) != 2 || got[0] != "api" || got[1] != "shared" {
		t.Fatalf("Scope.AffectedComponents = %#v", got)
	}
	if len(contract.Checks) != 2 {
		t.Fatalf("len(Checks) = %d, want 2", len(contract.Checks))
	}
	for _, check := range contract.Checks {
		if !check.Required {
			t.Fatalf("check %q required = false, want true", check.ID)
		}
	}
	if contract.Checks[0].ID != "api:test" || contract.Checks[1].ID != "shared:typecheck" {
		t.Fatalf("check IDs = %q, %q", contract.Checks[0].ID, contract.Checks[1].ID)
	}
}

func TestContractFromPlanRejectsEmptyPlan(t *testing.T) {
	if _, err := ContractFromPlan(repomanifest.CheckPlan{}); err == nil {
		t.Fatal("ContractFromPlan() error = nil, want empty plan error")
	}
}

func TestContractDigestChangesWhenValidationScopeChanges(t *testing.T) {
	first, err := ContractFromPlan(repomanifest.CheckPlan{
		ChangedFiles:       []string{"apps/api/a.go"},
		ChangedComponents:  []string{"api"},
		AffectedComponents: []string{"api"},
		Checks: []repomanifest.PlannedCheck{{
			Component: "api",
			Kind:      "test",
			Command:   repomanifest.Command{Run: "go test ./apps/api/..."},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ContractFromPlan(repomanifest.CheckPlan{
		ChangedFiles:       []string{"apps/api/b.go"},
		ChangedComponents:  []string{"api"},
		AffectedComponents: []string{"api"},
		Checks: []repomanifest.PlannedCheck{{
			Component: "api",
			Kind:      "test",
			Command:   repomanifest.Command{Run: "go test ./apps/api/..."},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest {
		t.Fatal("contract digest did not change when changed-file scope changed")
	}
}
