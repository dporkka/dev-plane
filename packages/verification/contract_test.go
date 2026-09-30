package verification

import (
	"strings"
	"testing"
	"time"
)

func TestParseContractRejectsUnknownFields(t *testing.T) {
	_, err := ParseContract([]byte(`{"version":1,"unknown":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ParseContract() error = %v, want unknown field error", err)
	}
}

func TestContractValidateRejectsDuplicateCheckIDs(t *testing.T) {
	contract := Contract{
		Version: 1,
		Checks: []Check{
			{ID: "unit", Command: "go test ./..."},
			{ID: "unit", Command: "go test ./..."},
		},
	}

	err := contract.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate check id") {
		t.Fatalf("Validate() error = %v, want duplicate check id error", err)
	}
}

func TestContractValidateRejectsContractsWithoutRequiredChecks(t *testing.T) {
	tests := []struct {
		name     string
		contract Contract
	}{
		{
			name:     "no checks",
			contract: Contract{Version: 1},
		},
		{
			name: "all checks optional",
			contract: Contract{
				Version: 1,
				Checks: []Check{
					{ID: "lint", Command: "go vet ./..."},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.contract.Validate()
			if err == nil || !strings.Contains(err.Error(), "required check") {
				t.Fatalf("Validate() error = %v, want required check error", err)
			}
		})
	}
}

func TestContractDigestChangesWhenVerificationRequirementsChange(t *testing.T) {
	contract := testContract()
	first, err := contract.Digest()
	if err != nil {
		t.Fatalf("Digest() first error = %v", err)
	}

	contract.Checks[0].Command = "go test -race ./..."
	second, err := contract.Digest()
	if err != nil {
		t.Fatalf("Digest() second error = %v", err)
	}

	if first == second {
		t.Fatalf("Digest() = %q for materially different contracts", first)
	}
}

func TestEvidenceFreshForExactCandidateAndVerificationInputs(t *testing.T) {
	contract := testContract()
	started := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	completed := started.Add(45 * time.Second)

	evidence, err := NewEvidence(EvidenceInput{
		TreeHash:          "tree-a",
		Contract:          contract,
		EnvironmentDigest: "env-a",
		RunnerIdentity:    "runner-1",
		Checks: []CheckResult{
			{ID: "unit", Passed: true, ExitCode: 0},
			{ID: "lint", Passed: true, ExitCode: 0},
		},
		StartedAt:   started,
		CompletedAt: completed,
	})
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}

	fresh, reason, err := evidence.FreshFor("tree-a", contract, "env-a")
	if err != nil {
		t.Fatalf("FreshFor() error = %v", err)
	}
	if !fresh {
		t.Fatalf("FreshFor() fresh = false, reason = %q", reason)
	}
	if reason != "" {
		t.Fatalf("FreshFor() reason = %q, want empty", reason)
	}
}

func TestEvidenceBecomesStaleWhenTreeContractOrEnvironmentChanges(t *testing.T) {
	contract := testContract()
	evidence, err := NewEvidence(EvidenceInput{
		TreeHash:          "tree-a",
		Contract:          contract,
		EnvironmentDigest: "env-a",
		RunnerIdentity:    "runner-1",
		Checks: []CheckResult{
			{ID: "unit", Passed: true},
			{ID: "lint", Passed: true},
		},
		StartedAt:         time.Now().UTC(),
		CompletedAt:       time.Now().UTC().Add(time.Second),
	})
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}

	tests := []struct {
		name              string
		treeHash          string
		contract          Contract
		environmentDigest string
		wantReason        string
	}{
		{
			name:              "tree changed",
			treeHash:          "tree-b",
			contract:          contract,
			environmentDigest: "env-a",
			wantReason:        "tree hash changed",
		},
		{
			name:              "contract changed",
			treeHash:          "tree-a",
			contract:          withChangedCommand(contract),
			environmentDigest: "env-a",
			wantReason:        "verification contract changed",
		},
		{
			name:              "environment changed",
			treeHash:          "tree-a",
			contract:          contract,
			environmentDigest: "env-b",
			wantReason:        "environment changed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fresh, reason, err := evidence.FreshFor(tt.treeHash, tt.contract, tt.environmentDigest)
			if err != nil {
				t.Fatalf("FreshFor() error = %v", err)
			}
			if fresh {
				t.Fatal("FreshFor() fresh = true, want false")
			}
			if reason != tt.wantReason {
				t.Fatalf("FreshFor() reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

func TestNewEvidenceRejectsFailedRequiredCheck(t *testing.T) {
	contract := testContract()

	_, err := NewEvidence(EvidenceInput{
		TreeHash:          "tree-a",
		Contract:          contract,
		EnvironmentDigest: "env-a",
		RunnerIdentity:    "runner-1",
		Checks:            []CheckResult{{ID: "unit", Passed: false, ExitCode: 1}},
		StartedAt:         time.Now().UTC(),
		CompletedAt:       time.Now().UTC().Add(time.Second),
	})
	if err == nil || !strings.Contains(err.Error(), "required check") {
		t.Fatalf("NewEvidence() error = %v, want required check failure", err)
	}
}

func testContract() Contract {
	return Contract{
		Version: 1,
		Environment: Environment{
			Setup: "go mod download",
		},
		Checks: []Check{
			{ID: "unit", Command: "go test ./...", Required: true, TimeoutSeconds: 300},
			{ID: "lint", Command: "golangci-lint run ./...", Required: true, TimeoutSeconds: 300},
		},
		ProtectedPaths: []string{"packages/auth/**", "migrations/**"},
		Artifacts:      []string{"coverage/**"},
	}
}

func withChangedCommand(contract Contract) Contract {
	updated := contract
	updated.Checks = append([]Check(nil), contract.Checks...)
	updated.Checks[0].Command = "go test -race ./..."
	return updated
}
