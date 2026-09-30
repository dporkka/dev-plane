package models

import "testing"

func TestOutcomeConstants(t *testing.T) {
	tests := []struct {
		got  Outcome
		want Outcome
	}{
		{OutcomePassed, "passed"},
		{OutcomeFailed, "failed"},
		{OutcomeError, "error"},
		{OutcomeCancelled, "cancelled"},
		{OutcomeSkipped, "skipped"},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("got %q, want %q", tt.got, tt.want)
			}
			if !tt.got.Valid() {
				t.Fatalf("expected %q to be valid", tt.got)
			}
		})
	}
}

func TestOutcomeValidRejectsUnknownValues(t *testing.T) {
	if Outcome("mystery").Valid() {
		t.Fatal("expected unknown outcome to be invalid")
	}
	if Outcome("").Valid() {
		t.Fatal("expected empty outcome to be invalid")
	}
}

func TestExecutionSnapshotDigestIsDeterministic(t *testing.T) {
	snapshot := ExecutionSnapshot{
		RecipeVersion:       "recipe-v3",
		PolicyVersion:       "policy-v7",
		ToolManifestDigest:  "sha256:tools",
		AgentProfileVersion: "implementer-v2",
		RepositoryBaseSHA:   "deadbeef",
		ModelRoute:          "bifrost/default",
		RuntimeImageDigest:  "sha256:runtime",
		VerificationProfile: "strict",
	}

	first, err := snapshot.Digest()
	if err != nil {
		t.Fatalf("first digest: %v", err)
	}
	second, err := snapshot.Digest()
	if err != nil {
		t.Fatalf("second digest: %v", err)
	}
	if first == "" {
		t.Fatal("expected non-empty digest")
	}
	if first != second {
		t.Fatalf("digest must be deterministic: %q != %q", first, second)
	}
}

func TestExecutionSnapshotDigestChangesWhenDefinitionChanges(t *testing.T) {
	base := ExecutionSnapshot{
		RecipeVersion:       "recipe-v3",
		PolicyVersion:       "policy-v7",
		ToolManifestDigest:  "sha256:tools",
		AgentProfileVersion: "implementer-v2",
		RepositoryBaseSHA:   "deadbeef",
		ModelRoute:          "bifrost/default",
		RuntimeImageDigest:  "sha256:runtime",
		VerificationProfile: "strict",
	}
	changed := base
	changed.PolicyVersion = "policy-v8"

	baseDigest, err := base.Digest()
	if err != nil {
		t.Fatalf("base digest: %v", err)
	}
	changedDigest, err := changed.Digest()
	if err != nil {
		t.Fatalf("changed digest: %v", err)
	}
	if baseDigest == changedDigest {
		t.Fatal("expected changed execution definition to produce a different digest")
	}
}

func TestAgentRunTerminalLifecycle(t *testing.T) {
	for _, status := range []string{
		AgentRunStatusCompleted,
		AgentRunStatusFailed,
		AgentRunStatusCancelled,
	} {
		run := AgentRun{Status: status}
		if !run.IsTerminal() {
			t.Fatalf("expected status %q to be terminal", status)
		}
	}

	run := AgentRun{Status: AgentRunStatusRunning}
	if run.IsTerminal() {
		t.Fatal("running run must not be terminal")
	}
}

func TestAgentRunHasTerminalOutcome(t *testing.T) {
	run := AgentRun{Outcome: OutcomePassed}
	if !run.HasOutcome() {
		t.Fatal("expected passed run to have outcome")
	}
	run.Outcome = ""
	if run.HasOutcome() {
		t.Fatal("expected empty outcome to mean no outcome yet")
	}
}
