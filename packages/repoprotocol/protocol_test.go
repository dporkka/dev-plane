package repoprotocol

import (
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Version: 1,
		Commands: Commands{
			Doctor: "make doctor",
			Dev:    "make dev",
			Logs:   "make logs",
		},
		Verification: map[string]VerificationProfile{
			"fast":    {Command: "make verify-fast"},
			"changed": {Command: "make verify-changed"},
			"full":    {Command: "make ci"},
		},
		Work: WorkConfig{
			Isolation:           IsolationWorktree,
			MaxParallelCost:     4,
			RequireCleanHandoff: true,
		},
		Review: ReviewConfig{
			Independent: true,
			ExactHead:   true,
		},
		Risk: []RiskRule{
			{
				Paths:    []string{"auth/**", "db/migrations/**"},
				Level:    RiskHigh,
				Requires: []string{"full", GateIndependentReview, GateHumanApproval},
			},
		},
	}
}

func TestConfigValidateAcceptsRepositoryAgnosticContract(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRejectsUnsupportedVersion(t *testing.T) {
	cfg := validConfig()
	cfg.Version = 2

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("Validate() error = %v, want unsupported version", err)
	}
}

func TestConfigValidateRejectsUnknownRequiredGate(t *testing.T) {
	cfg := validConfig()
	cfg.Risk[0].Requires = []string{"does-not-exist"}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown gate") {
		t.Fatalf("Validate() error = %v, want unknown gate", err)
	}
}

func TestConfigValidateRejectsRiskPathEscapingRepository(t *testing.T) {
	cfg := validConfig()
	cfg.Risk[0].Paths = []string{"../secrets/**"}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "repository-relative") {
		t.Fatalf("Validate() error = %v, want repository-relative path error", err)
	}
}

func TestRequiredGatesForPathsAppliesNestedRiskRulesAndDeduplicates(t *testing.T) {
	cfg := validConfig()
	cfg.Risk = append(cfg.Risk, RiskRule{
		Paths:    []string{"auth/session.go"},
		Level:    RiskCritical,
		Requires: []string{"full", GateIndependentReview},
	})

	got := cfg.RequiredGatesForPaths([]string{"apps/web/page.tsx", "auth/session.go"})
	want := []string{"full", GateIndependentReview, GateHumanApproval}
	if len(got) != len(want) {
		t.Fatalf("RequiredGatesForPaths() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RequiredGatesForPaths() = %#v, want %#v", got, want)
		}
	}
}

func TestEvidenceBundleValidateHeadBindsEvidenceToExactCandidate(t *testing.T) {
	bundle := EvidenceBundle{
		BaseSHA: "base123",
		HeadSHA: "head456",
		Gates: []GateEvidence{
			{Name: "changed", Status: GatePassed},
			{Name: GateIndependentReview, Status: GatePassed},
		},
	}

	if err := bundle.ValidateHead("head456"); err != nil {
		t.Fatalf("ValidateHead() error = %v", err)
	}
	if err := bundle.ValidateHead("new-head"); err == nil || !strings.Contains(err.Error(), "head mismatch") {
		t.Fatalf("ValidateHead() error = %v, want head mismatch", err)
	}
}

func TestEvidenceBundleValidateHeadRejectsUnpassedGate(t *testing.T) {
	bundle := EvidenceBundle{
		BaseSHA: "base123",
		HeadSHA: "head456",
		Gates: []GateEvidence{
			{Name: "changed", Status: GateFailed},
		},
	}

	err := bundle.ValidateHead("head456")
	if err == nil || !strings.Contains(err.Error(), "not passed") {
		t.Fatalf("ValidateHead() error = %v, want not passed", err)
	}
}

func TestWorkItemValidateAcceptsDurableDependencyAwareWork(t *testing.T) {
	item := WorkItem{
		ID:                 "DEV-101",
		Repository:         "dporkka/dev-plane",
		Objective:          "Introduce the repository protocol",
		AcceptanceCriteria: []string{"protocol validates"},
		DependsOn:          []string{"DEV-100"},
		OwnershipPaths:     []string{"packages/repoprotocol/**"},
		Risk:               RiskMedium,
		Cost:               2,
		RequiredGates:      []string{"changed", GateIndependentReview},
		BaseSHA:            "abc123",
		State:              WorkReady,
	}

	if err := item.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !item.CanClaim() {
		t.Fatal("CanClaim() = false, want true for ready work")
	}
}

func TestWorkItemValidateRejectsInvalidOwnershipPath(t *testing.T) {
	item := WorkItem{
		ID:             "DEV-101",
		Repository:     "dporkka/dev-plane",
		Objective:      "Bad ownership scope",
		OwnershipPaths: []string{"../../etc/**"},
		Risk:           RiskMedium,
		Cost:           1,
		BaseSHA:        "abc123",
		State:          WorkReady,
	}

	err := item.Validate()
	if err == nil || !strings.Contains(err.Error(), "repository-relative") {
		t.Fatalf("Validate() error = %v, want repository-relative path error", err)
	}
}

func TestParseConfigYAMLRejectsUnknownFieldsAndValidates(t *testing.T) {
	raw := []byte(`version: 1
commands:
  doctor: make doctor
verification:
  changed:
    command: make verify-changed
work:
  isolation: worktree
  max_parallel_cost: 4
review:
  independent: true
  exact_head: true
risk:
  - paths:
      - auth/**
    level: high
    requires:
      - changed
      - independent_review
`)

	cfg, err := ParseConfigYAML(raw)
	if err != nil {
		t.Fatalf("ParseConfigYAML() error = %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("ParseConfigYAML().Version = %d, want %d", cfg.Version, CurrentVersion)
	}

	unknown := append(raw, []byte("typo_field: true\n")...)
	if _, err := ParseConfigYAML(unknown); err == nil || !strings.Contains(err.Error(), "field typo_field not found") {
		t.Fatalf("ParseConfigYAML(unknown) error = %v, want strict unknown-field error", err)
	}
}

func TestConfigValidateAcceptsBrowserVerificationProfile(t *testing.T) {
	cfg := validConfig()
	cfg.Verification["browser"] = VerificationProfile{
		Browser: &BrowserVerificationProfile{
			Command:       "pnpm exec playwright test tests/e2e/checkout.spec.ts",
			ReportPath:    "artifacts/browser/report.json",
			ArtifactPaths: []string{"artifacts/browser/checkout.png", "artifacts/browser/trace.zip"},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRejectsVerificationProfileWithCommandAndBrowser(t *testing.T) {
	cfg := validConfig()
	cfg.Verification["ambiguous"] = VerificationProfile{
		Command: "make verify",
		Browser: &BrowserVerificationProfile{
			Command: "pnpm exec playwright test",
		},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("Validate() error = %v, want exactly-one verification mode error", err)
	}
}

func TestConfigValidateRejectsBrowserArtifactEscapingRepository(t *testing.T) {
	cfg := validConfig()
	cfg.Verification["browser"] = VerificationProfile{
		Browser: &BrowserVerificationProfile{
			Command:       "pnpm exec playwright test",
			ArtifactPaths: []string{"../outside/trace.zip"},
		},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "repository-relative") {
		t.Fatalf("Validate() error = %v, want repository-relative artifact path error", err)
	}
}
