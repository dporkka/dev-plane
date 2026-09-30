package verifier

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

type fakeRuntime struct {
	config        []byte
	headResponses []string
	commands      []runtimes.Command
	results       map[string]*runtimes.CommandResult
	errors        map[string]error
}

func (f *fakeRuntime) ReadFile(_ context.Context, _ string, path string) ([]byte, error) {
	if path != "devplane.yaml" {
		return nil, errors.New("unexpected file")
	}
	return f.config, nil
}

func (f *fakeRuntime) ExecuteCommand(_ context.Context, _ string, cmd runtimes.Command) (*runtimes.CommandResult, error) {
	f.commands = append(f.commands, cmd)
	if len(cmd.Args) == 3 && reflect.DeepEqual(cmd.Args, []string{"git", "rev-parse", "HEAD"}) {
		if len(f.headResponses) == 0 {
			return nil, errors.New("no head response")
		}
		head := f.headResponses[0]
		f.headResponses = f.headResponses[1:]
		return &runtimes.CommandResult{Stdout: head + "\n", ExitCode: 0}, nil
	}
	if err := f.errors[cmd.Command]; err != nil {
		return nil, err
	}
	if result := f.results[cmd.Command]; result != nil {
		copy := *result
		return &copy, nil
	}
	return &runtimes.CommandResult{ExitCode: 0}, nil
}

type fakeStore struct {
	bundles []repoprotocol.EvidenceBundle
}

func (s *fakeStore) PutEvidenceBundle(_ context.Context, bundle repoprotocol.EvidenceBundle) error {
	s.bundles = append(s.bundles, bundle)
	return nil
}

func verifierConfig() []byte {
	return []byte(`version: 1
verification:
  changed:
    command: make verify-changed
    timeout_seconds: 7
  full:
    command: make ci
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
      - full
      - independent_review
      - human_approval
`)
}

func verifyingWorkItem() repoprotocol.WorkItem {
	return repoprotocol.WorkItem{
		ID:             "DEV-401",
		Repository:     "dporkka/dev-plane",
		Objective:      "Verify exact repository head",
		OwnershipPaths: []string{"auth/**"},
		Risk:           repoprotocol.RiskHigh,
		Cost:           1,
		RequiredGates:  []string{"changed", repoprotocol.GateIndependentReview},
		BaseSHA:        "base123",
		State:          repoprotocol.WorkVerifying,
	}
}

func TestVerifyExecutesRequiredProfilesPersistsExactHeadEvidenceAndReturnsPendingReservedGates(t *testing.T) {
	runtime := &fakeRuntime{
		config:        verifierConfig(),
		headResponses: []string{"head456", "head456", "head456"},
		results: map[string]*runtimes.CommandResult{
			"make verify-changed": {Stdout: "changed ok", ExitCode: 0, Duration: 2 * time.Second},
			"make ci":             {Stdout: "full ok", ExitCode: 0, Duration: 3 * time.Second},
		},
		errors: map[string]error{},
	}
	store := &fakeStore{}
	v := New(runtime, store)

	result, err := v.Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      verifyingWorkItem(),
		CandidateHead: "head456",
		ChangedPaths:  []string{"auth/session.go"},
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	if got, want := result.PendingGates, []string{repoprotocol.GateIndependentReview, repoprotocol.GateHumanApproval}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingGates = %#v, want %#v", got, want)
	}
	if len(result.Evidence.Gates) != 2 {
		t.Fatalf("len(Evidence.Gates) = %d, want 2", len(result.Evidence.Gates))
	}
	if result.Evidence.HeadSHA != "head456" || result.Evidence.BaseSHA != "base123" || result.Evidence.WorkItemID != "DEV-401" {
		t.Fatalf("Evidence identity = %#v", result.Evidence)
	}
	for _, gate := range result.Evidence.Gates {
		if gate.Status != repoprotocol.GatePassed {
			t.Fatalf("gate %s status = %s, want passed", gate.Name, gate.Status)
		}
	}
	if len(store.bundles) != 1 || !reflect.DeepEqual(store.bundles[0], result.Evidence) {
		t.Fatalf("persisted bundles = %#v, evidence = %#v", store.bundles, result.Evidence)
	}

	var profileCommands []runtimes.Command
	for _, cmd := range runtime.commands {
		if cmd.Command != "" {
			profileCommands = append(profileCommands, cmd)
		}
	}
	if len(profileCommands) != 2 {
		t.Fatalf("profile commands = %#v", profileCommands)
	}
	if profileCommands[0].Command != "make verify-changed" || profileCommands[0].Timeout != 7*time.Second || !profileCommands[0].UnsafeShell {
		t.Fatalf("changed command = %#v", profileCommands[0])
	}
	if profileCommands[1].Command != "make ci" {
		t.Fatalf("full command = %#v", profileCommands[1])
	}
}

func TestVerifyRejectsStaleCandidateBeforeRunningProfiles(t *testing.T) {
	runtime := &fakeRuntime{
		config:        verifierConfig(),
		headResponses: []string{"different-head"},
		results:       map[string]*runtimes.CommandResult{},
		errors:        map[string]error{},
	}
	store := &fakeStore{}

	_, err := New(runtime, store).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      verifyingWorkItem(),
		CandidateHead: "head456",
		ChangedPaths:  []string{"auth/session.go"},
	})
	if err == nil || !strings.Contains(err.Error(), "head mismatch") {
		t.Fatalf("Verify() error = %v, want head mismatch", err)
	}
	if len(store.bundles) != 0 {
		t.Fatalf("persisted stale evidence: %#v", store.bundles)
	}
	for _, cmd := range runtime.commands {
		if cmd.Command != "" {
			t.Fatalf("verification profile ran for stale candidate: %#v", cmd)
		}
	}
}

func TestVerifyRejectsHeadMutationDuringGateAndDoesNotPersist(t *testing.T) {
	runtime := &fakeRuntime{
		config:        verifierConfig(),
		headResponses: []string{"head456", "mutated-head"},
		results: map[string]*runtimes.CommandResult{
			"make verify-changed": {Stdout: "changed ok", ExitCode: 0},
		},
		errors: map[string]error{},
	}
	store := &fakeStore{}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"changed"}

	_, err := New(runtime, store).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err == nil || !strings.Contains(err.Error(), "head changed during verification") {
		t.Fatalf("Verify() error = %v, want mutation error", err)
	}
	if len(store.bundles) != 0 {
		t.Fatalf("persisted evidence for mutated head: %#v", store.bundles)
	}
}

func TestVerifyPersistsFailedGateAndStops(t *testing.T) {
	runtime := &fakeRuntime{
		config:        verifierConfig(),
		headResponses: []string{"head456", "head456"},
		results: map[string]*runtimes.CommandResult{
			"make verify-changed": {Stdout: "failure output", ExitCode: 2},
			"make ci":             {Stdout: "must not run", ExitCode: 0},
		},
		errors: map[string]error{},
	}
	store := &fakeStore{}

	_, err := New(runtime, store).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      verifyingWorkItem(),
		CandidateHead: "head456",
		ChangedPaths:  []string{"auth/session.go"},
	})
	if err == nil || !strings.Contains(err.Error(), "gate changed failed") {
		t.Fatalf("Verify() error = %v, want gate failure", err)
	}
	if len(store.bundles) != 1 || len(store.bundles[0].Gates) != 1 {
		t.Fatalf("persisted failed evidence = %#v", store.bundles)
	}
	if store.bundles[0].Gates[0].Status != repoprotocol.GateFailed {
		t.Fatalf("failed gate status = %s", store.bundles[0].Gates[0].Status)
	}
	for _, cmd := range runtime.commands {
		if cmd.Command == "make ci" {
			t.Fatal("full gate ran after changed gate failure")
		}
	}
}

func TestVerifyRejectsUnknownWorkItemGate(t *testing.T) {
	runtime := &fakeRuntime{
		config:        verifierConfig(),
		headResponses: []string{"head456"},
		results:       map[string]*runtimes.CommandResult{},
		errors:        map[string]error{},
	}
	store := &fakeStore{}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"typo-gate"}

	_, err := New(runtime, store).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown required gate") {
		t.Fatalf("Verify() error = %v, want unknown gate error", err)
	}
}
