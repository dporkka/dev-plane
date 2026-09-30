package models

import (
	"strings"
	"testing"
)

func validRunManifestInput() RunManifestInput {
	maxCost := 12.5
	return RunManifestInput{
		RunID:           "run-1",
		TaskID:          "task-1",
		RepositoryID:    "repo-1",
		BaseSHA:         "abc123",
		AgentRole:       "implementer",
		ExecutionClass:  "standard",
		RuntimeProvider: "docker",
		Resources: RunResourceLimits{
			CPUMillis:       2000,
			MemoryMB:        4096,
			DiskMB:          10240,
			WallTimeSeconds: 1800,
		},
		Authority: RunAuthority{
			Network:    false,
			Secrets:    []string{"github-token", "npm-token"},
			Operations: []string{"write_file", "read_file", "run_tests"},
		},
		Budget: RunBudgetLimits{
			MaxCostUSD:        &maxCost,
			MaxRuntimeMinutes: 30,
			MaxModelCalls:     100,
			MaxToolCalls:      500,
			MaxShellCommands:  100,
		},
		RequiredGates:      []string{"full", "independent_review"},
		AcceptanceCriteria: []string{"tests pass", "browser checkout succeeds"},
	}
}

func TestNewRunManifestProducesStableDigestAcrossSetOrdering(t *testing.T) {
	first, err := NewRunManifest(validRunManifestInput())
	if err != nil {
		t.Fatalf("NewRunManifest() error = %v", err)
	}

	input := validRunManifestInput()
	input.Authority.Secrets = []string{"npm-token", "github-token", "github-token"}
	input.Authority.Operations = []string{"run_tests", "read_file", "write_file", "read_file"}
	input.RequiredGates = []string{"independent_review", "full", "full"}
	second, err := NewRunManifest(input)
	if err != nil {
		t.Fatalf("NewRunManifest(reordered) error = %v", err)
	}

	if first.Digest == "" || second.Digest == "" {
		t.Fatal("manifest digest must not be empty")
	}
	if first.Digest != second.Digest {
		t.Fatalf("digest changed for set ordering: %s != %s", first.Digest, second.Digest)
	}
	if len(second.Authority.Secrets) != 2 || len(second.Authority.Operations) != 3 || len(second.RequiredGates) != 2 {
		t.Fatalf("normalized set fields = secrets=%v ops=%v gates=%v", second.Authority.Secrets, second.Authority.Operations, second.RequiredGates)
	}
}

func TestRunManifestVerifyDigestDetectsMutation(t *testing.T) {
	manifest, err := NewRunManifest(validRunManifestInput())
	if err != nil {
		t.Fatalf("NewRunManifest() error = %v", err)
	}
	if err := manifest.VerifyDigest(); err != nil {
		t.Fatalf("VerifyDigest() error = %v", err)
	}

	manifest.Resources.MemoryMB = 8192
	err = manifest.VerifyDigest()
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("VerifyDigest() error = %v, want digest mismatch", err)
	}
}

func TestNewRunManifestRejectsMissingExecutionIdentity(t *testing.T) {
	input := validRunManifestInput()
	input.BaseSHA = ""

	_, err := NewRunManifest(input)
	if err == nil || !strings.Contains(err.Error(), "base_sha") {
		t.Fatalf("NewRunManifest() error = %v, want base_sha error", err)
	}
}

func TestNewRunManifestRejectsInvalidResourceLimits(t *testing.T) {
	input := validRunManifestInput()
	input.Resources.MemoryMB = 0

	_, err := NewRunManifest(input)
	if err == nil || !strings.Contains(err.Error(), "memory_mb") {
		t.Fatalf("NewRunManifest() error = %v, want memory_mb error", err)
	}
}

func TestRunManifestDigestChangesWhenAuthorityChanges(t *testing.T) {
	first, err := NewRunManifest(validRunManifestInput())
	if err != nil {
		t.Fatalf("NewRunManifest() error = %v", err)
	}
	input := validRunManifestInput()
	input.Authority.Network = true
	second, err := NewRunManifest(input)
	if err != nil {
		t.Fatalf("NewRunManifest(network) error = %v", err)
	}
	if first.Digest == second.Digest {
		t.Fatal("digest must change when runtime authority changes")
	}
}
