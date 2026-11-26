package main

import (
	"strings"
	"testing"
)

func TestDecisionExitCode(t *testing.T) {
	if got := decisionExitCode(true); got != 0 {
		t.Fatalf("promote=true exit code = %d, want 0", got)
	}
	if got := decisionExitCode(false); got == 0 {
		t.Fatal("promote=false must return a non-zero exit code")
	}
}

func TestValidatePromotionProvenanceAcceptsBoundSuite(t *testing.T) {
	input := validProvenancedSuite()
	if err := validatePromotionProvenance(input); err != nil {
		t.Fatalf("validatePromotionProvenance() error = %v", err)
	}
}

func TestValidatePromotionProvenanceRejectsMissingProvenanceVersion(t *testing.T) {
	input := validProvenancedSuite()
	input.ProvenanceVersion = 0
	assertValidationErrorContains(t, input, "provenance_version")
}

func TestValidatePromotionProvenanceRejectsInvalidCorpusHash(t *testing.T) {
	input := validProvenancedSuite()
	input.Corpus.SHA256 = "not-a-sha256"
	assertValidationErrorContains(t, input, "corpus sha256")
}

func TestValidatePromotionProvenanceRejectsBackendVersionError(t *testing.T) {
	input := validProvenancedSuite()
	input.Tools["codebase-memory"] = "error: version unavailable"
	assertValidationErrorContains(t, input, "codebase-memory")
}

func TestValidatePromotionProvenanceRejectsScenarioRevisionMismatch(t *testing.T) {
	input := validProvenancedSuite()
	input.Scenarios[0].Revision = "different"
	assertValidationErrorContains(t, input, "revision")
}

func TestValidatePromotionProvenanceRejectsMissingRepositoryBinding(t *testing.T) {
	input := validProvenancedSuite()
	delete(input.Repositories, "owner/repo")
	assertValidationErrorContains(t, input, "repository provenance")
}

func validProvenancedSuite() inputSuite {
	const corpusSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const revision = "0123456789abcdef0123456789abcdef01234567"
	return inputSuite{
		ProvenanceVersion: 2,
		Corpus: inputCorpus{
			Version: 9,
			SHA256: corpusSHA,
		},
		Tools: map[string]string{
			"codebase-memory": "codebase-memory-mcp 0.11.0",
			"gitnexus":        "gitnexus 1.6.12",
		},
		Repositories: map[string]inputRepository{
			"owner/repo": {Path: "/repos/repo", Revision: revision},
		},
		Scenarios: []inputScenario{
			{
				ID:         "s1",
				Repository: "owner/repo",
				Revision:   revision,
				Expected:   []string{"src/a.go"},
				Runs: []inputRun{
					{Backend: "codebase-memory", Results: []string{"src/a.go"}},
					{Backend: "gitnexus", Results: []string{"src/a.go"}},
				},
			},
		},
	}
}

func assertValidationErrorContains(t *testing.T, input inputSuite, want string) {
	t.Helper()
	err := validatePromotionProvenance(input)
	if err == nil {
		t.Fatalf("expected validation error containing %q", want)
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}
