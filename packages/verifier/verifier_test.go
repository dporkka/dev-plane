package verifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	files         map[string][]byte
	headResponses []string
	commands      []runtimes.Command
	results       map[string]*runtimes.CommandResult
	errors        map[string]error
}

func (f *fakeRuntime) ReadFile(_ context.Context, _ string, path string) ([]byte, error) {
	if path == "devplane.yaml" {
		return f.config, nil
	}
	if data, ok := f.files[path]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, errors.New("unexpected file")
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
	bundles   []repoprotocol.EvidenceBundle
	workItems []repoprotocol.WorkItem
}

func (s *fakeStore) PutEvidenceBundle(_ context.Context, bundle repoprotocol.EvidenceBundle) error {
	s.bundles = append(s.bundles, bundle)
	return nil
}

func (s *fakeStore) PutWorkItem(_ context.Context, item repoprotocol.WorkItem) error {
	s.workItems = append(s.workItems, item)
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

func TestVerifyAdvancesDurableWorkToReviewingWhenReservedGatesRemain(t *testing.T) {
	runtime := &fakeRuntime{
		config:        verifierConfig(),
		headResponses: []string{"head456", "head456", "head456"},
		results: map[string]*runtimes.CommandResult{
			"make verify-changed": {ExitCode: 0},
			"make ci":             {ExitCode: 0},
		},
		errors: map[string]error{},
	}
	store := &fakeStore{}

	result, err := New(runtime, store).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      verifyingWorkItem(),
		CandidateHead: "head456",
		ChangedPaths:  []string{"auth/session.go"},
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if result.NextState != repoprotocol.WorkReviewing {
		t.Fatalf("NextState = %s, want %s", result.NextState, repoprotocol.WorkReviewing)
	}
	if len(store.workItems) != 1 || store.workItems[0].State != repoprotocol.WorkReviewing {
		t.Fatalf("persisted work items = %#v", store.workItems)
	}
}

func TestVerifyAdvancesDurableWorkToReadyToLandWhenNoReservedGatesRemain(t *testing.T) {
	runtime := &fakeRuntime{
		config: []byte(`version: 1
verification:
  changed:
    command: make verify-changed
work:
  isolation: worktree
  max_parallel_cost: 1
`),
		headResponses: []string{"head456", "head456"},
		results: map[string]*runtimes.CommandResult{
			"make verify-changed": {ExitCode: 0},
		},
		errors: map[string]error{},
	}
	store := &fakeStore{}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"changed"}

	result, err := New(runtime, store).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if result.NextState != repoprotocol.WorkReadyToLand {
		t.Fatalf("NextState = %s, want %s", result.NextState, repoprotocol.WorkReadyToLand)
	}
	if len(store.workItems) != 1 || store.workItems[0].State != repoprotocol.WorkReadyToLand {
		t.Fatalf("persisted work items = %#v", store.workItems)
	}
}

func TestVerifyDoesNotAdvanceWorkWhenExecutableGateFails(t *testing.T) {
	runtime := &fakeRuntime{
		config:        verifierConfig(),
		headResponses: []string{"head456", "head456"},
		results: map[string]*runtimes.CommandResult{
			"make verify-changed": {ExitCode: 1},
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
	if err == nil {
		t.Fatal("Verify() error = nil, want gate failure")
	}
	if len(store.workItems) != 0 {
		t.Fatalf("failed verification advanced work: %#v", store.workItems)
	}
}


func TestVerifyExecutesBrowserProfileAndPersistsHashedArtifacts(t *testing.T) {
	command := "pnpm exec playwright test tests/e2e/checkout.spec.ts"
	screenshot := []byte("fake-png")
	sum := sha256.Sum256(screenshot)

	runtime := &fakeRuntime{
		config: []byte(`version: 1
verification:
  browser:
    browser:
      command: pnpm exec playwright test tests/e2e/checkout.spec.ts
      report_path: artifacts/browser/report.txt
      artifact_paths:
        - artifacts/browser/checkout.png
work:
  isolation: worktree
  max_parallel_cost: 1
`),
		files: map[string][]byte{
			"artifacts/browser/report.txt":  []byte("2 passed; console errors: 0"),
			"artifacts/browser/checkout.png": screenshot,
		},
		headResponses: []string{"head456", "head456"},
		results: map[string]*runtimes.CommandResult{
			command: {Stdout: "playwright passed", ExitCode: 0},
		},
		errors: map[string]error{},
	}
	store := &fakeStore{}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"browser"}

	result, err := New(runtime, store).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(result.Evidence.Gates) != 1 {
		t.Fatalf("len(Evidence.Gates) = %d, want 1", len(result.Evidence.Gates))
	}
	gate := result.Evidence.Gates[0]
	if gate.Kind != VerificationKindBrowser {
		t.Fatalf("gate.Kind = %q, want %q", gate.Kind, VerificationKindBrowser)
	}
	if !strings.Contains(gate.Output, "playwright passed") || !strings.Contains(gate.Output, "2 passed; console errors: 0") {
		t.Fatalf("gate.Output = %q, want command and browser report output", gate.Output)
	}
	if len(gate.Artifacts) != 1 {
		t.Fatalf("len(gate.Artifacts) = %d, want 1", len(gate.Artifacts))
	}
	artifact := gate.Artifacts[0]
	if artifact.Path != "artifacts/browser/checkout.png" {
		t.Fatalf("artifact.Path = %q", artifact.Path)
	}
	if artifact.SizeBytes != int64(len(screenshot)) {
		t.Fatalf("artifact.SizeBytes = %d, want %d", artifact.SizeBytes, len(screenshot))
	}
	if artifact.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("artifact.SHA256 = %q, want %q", artifact.SHA256, hex.EncodeToString(sum[:]))
	}
}
