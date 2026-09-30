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
