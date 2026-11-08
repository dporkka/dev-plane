package verifier

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

func TestVerifyRecordsCompletedGateExecutionWithExactSessionAndExit(t *testing.T) {
	runtime := &fakeRuntime{
		config:        verifierConfig(),
		headResponses: []string{"head456", "head456"},
		results: map[string]*runtimes.CommandResult{
			"make verify-changed": {Stdout: "verified", ExitCode: 0, Duration: 1250 * time.Millisecond},
		},
	}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"changed"}
	result, err := New(runtime, &fakeStore{}).Verify(context.Background(), Request{
		SessionID: "session-1", WorkItem: item, CandidateHead: "head456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Evidence.Gates) != 1 {
		t.Fatalf("gates=%+v", result.Evidence.Gates)
	}
	execution := result.Evidence.Gates[0].Execution
	if execution == nil || execution.SessionID != "session-1" || execution.AttemptID == "" ||
		execution.Outcome != repoprotocol.GateExecutionCompleted || execution.ExitCode == nil ||
		*execution.ExitCode != 0 || execution.DurationMillis != 1250 ||
		execution.StartedAt.IsZero() || execution.FinishedAt.Before(execution.StartedAt) {
		t.Fatalf("missing structured executable evidence: %+v", execution)
	}
	if err := result.Evidence.ValidateHead("head456"); err != nil {
		t.Fatalf("persisted execution should be valid: %v", err)
	}
}

func TestVerifyDistinguishesRunnerFailureFromTestFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		result   *runtimes.CommandResult
		runErr   error
		outcome  repoprotocol.GateExecutionOutcome
		exitCode *int
	}{
		{name: "test failed", result: &runtimes.CommandResult{ExitCode: 2}, outcome: repoprotocol.GateExecutionCompleted, exitCode: ptrExitCode(2)},
		{name: "runner unavailable", runErr: errors.New("runner unreachable"), outcome: repoprotocol.GateExecutionRunnerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := &fakeRuntime{config: verifierConfig(), headResponses: []string{"head456", "head456"},
				results: map[string]*runtimes.CommandResult{}, errors: map[string]error{}}
			runtime.results["make verify-changed"] = tc.result
			runtime.errors["make verify-changed"] = tc.runErr
			item := verifyingWorkItem()
			item.RequiredGates = []string{"changed"}
			store := &fakeStore{}
			_, err := New(runtime, store).Verify(context.Background(), Request{
				SessionID: "session-1", WorkItem: item, CandidateHead: "head456",
			})
			if err == nil || !strings.Contains(err.Error(), "gate changed failed") {
				t.Fatalf("Verify() error=%v", err)
			}
			if len(store.bundles) != 1 || len(store.bundles[0].Gates) != 1 {
				t.Fatalf("missing failure evidence: %+v", store.bundles)
			}
			gate := store.bundles[0].Gates[0]
			if gate.Status != repoprotocol.GateFailed || gate.Execution == nil ||
				gate.Execution.Outcome != tc.outcome || gate.Execution.AttemptID == "" ||
				!sameExitCode(gate.Execution.ExitCode, tc.exitCode) {
				t.Fatalf("failure misclassified: %+v", gate)
			}
		})
	}
}

func ptrExitCode(n int) *int { return &n }
func sameExitCode(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
