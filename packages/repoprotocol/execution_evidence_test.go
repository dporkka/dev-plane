package repoprotocol

import (
	"strings"
	"testing"
	"time"
)

func TestValidateHeadRejectsPassedGateWithoutExecutableProof(t *testing.T) {
	bundle := EvidenceBundle{
		BaseSHA: "base123", HeadSHA: "head456",
		Gates: []GateEvidence{{Name: "changed", Status: GatePassed, Command: "make test"}},
	}
	if err := bundle.ValidateHead("head456"); err == nil || !strings.Contains(err.Error(), "execution evidence") {
		t.Fatalf("ValidateHead() error = %v, want missing execution evidence", err)
	}
}

func TestValidateHeadRejectsContradictoryExecutionProof(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	zero := 0
	cases := []struct {
		name   string
		mutate func(*GateExecution)
	}{
		{"runner error", func(e *GateExecution) { e.Outcome = GateExecutionRunnerError }},
		{"failed exit", func(e *GateExecution) { x := 2; e.ExitCode = &x }},
		{"missing result", func(e *GateExecution) { e.ExitCode = nil }},
		{"missing attempt", func(e *GateExecution) { e.AttemptID = "" }},
		{"missing session", func(e *GateExecution) { e.SessionID = "" }},
		{"reversed times", func(e *GateExecution) { e.FinishedAt = start.Add(-time.Second) }},
		{"negative duration", func(e *GateExecution) { e.DurationMillis = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			execution := &GateExecution{SessionID: "session-1", AttemptID: "attempt-1",
				Outcome: GateExecutionCompleted, ExitCode: &zero, StartedAt: start,
				FinishedAt: start.Add(time.Second), DurationMillis: 1000}
			tc.mutate(execution)
			bundle := EvidenceBundle{BaseSHA: "base123", HeadSHA: "head456",
				Gates: []GateEvidence{{Name: "changed", Status: GatePassed, Execution: execution}}}
			if err := bundle.ValidateHead("head456"); err == nil {
				t.Fatalf("passed gate accepted contradictory execution: %+v", execution)
			}
		})
	}
}

func TestValidateHeadAcceptsCompletedCommandAndNonExecutableApproval(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	code := 0
	bundle := EvidenceBundle{BaseSHA: "base123", HeadSHA: "head456",
		Gates: []GateEvidence{
			{Name: "changed", Status: GatePassed, Execution: &GateExecution{
				SessionID: "session-1", AttemptID: "attempt-1",
				Outcome: GateExecutionCompleted, ExitCode: &code,
				StartedAt: start, FinishedAt: start.Add(time.Second), DurationMillis: 1000,
			}},
			{Name: GateIndependentReview, Status: GatePassed},
		}}
	if err := bundle.ValidateHead("head456"); err != nil {
		t.Fatalf("ValidateHead() error = %v", err)
	}
}
