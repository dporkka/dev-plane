package verifier

import (
	"context"
	"errors"
	"strings"
	"testing"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

type fakeWorkspaceRuntime struct {
	*fakeRuntime
	createSession *runtimes.Session
	createErr     error
	destroyErr    error
	createCalls   []runtimes.CreateRequest
	destroyCalls  []string
}

func (f *fakeWorkspaceRuntime) CreateWorkspace(_ context.Context, req runtimes.CreateRequest) (*runtimes.Session, error) {
	f.createCalls = append(f.createCalls, req)
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createSession, nil
}

func (f *fakeWorkspaceRuntime) DestroyWorkspace(_ context.Context, sessionID string) error {
	f.destroyCalls = append(f.destroyCalls, sessionID)
	return f.destroyErr
}

func TestRunnerCreatesVerifiesAndDestroysEphemeralWorkspace(t *testing.T) {
	runtime := &fakeWorkspaceRuntime{
		fakeRuntime: &fakeRuntime{
			config:        verifierConfig(),
			headResponses: []string{"head456", "head456"},
			results: map[string]*runtimes.CommandResult{
				"make verify-changed": {ExitCode: 0},
			},
			errors: map[string]error{},
		},
		createSession: &runtimes.Session{ID: "workspace-1", Status: "ready", Provider: "nulang"},
	}
	store := &fakeStore{}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"changed"}

	result, err := NewRunner(runtime, store).Run(context.Background(), RunRequest{
		Workspace: runtimes.CreateRequest{
			RepositoryID:   "dporkka/dev-plane",
			CloneURL:       "https://github.com/dporkka/dev-plane.git",
			Branch:         "head456",
			IdempotencyKey: "verify:DEV-401:head456",
			Capabilities:   runtimes.RuntimeCapabilities{Network: false},
		},
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.SessionID != "workspace-1" {
		t.Fatalf("SessionID = %q, want workspace-1", result.SessionID)
	}
	if result.Verification.NextState != repoprotocol.WorkReadyToLand {
		t.Fatalf("NextState = %s, want %s", result.Verification.NextState, repoprotocol.WorkReadyToLand)
	}
	if len(runtime.createCalls) != 1 || runtime.createCalls[0].IdempotencyKey != "verify:DEV-401:head456" {
		t.Fatalf("create calls = %#v", runtime.createCalls)
	}
	if len(runtime.destroyCalls) != 1 || runtime.destroyCalls[0] != "workspace-1" {
		t.Fatalf("destroy calls = %#v", runtime.destroyCalls)
	}
}

func TestRunnerDestroysWorkspaceWhenVerificationFails(t *testing.T) {
	runtime := &fakeWorkspaceRuntime{
		fakeRuntime: &fakeRuntime{
			config:        verifierConfig(),
			headResponses: []string{"head456", "head456"},
			results: map[string]*runtimes.CommandResult{
				"make verify-changed": {ExitCode: 2, Stderr: "broken"},
			},
			errors: map[string]error{},
		},
		createSession: &runtimes.Session{ID: "workspace-2", Status: "ready"},
	}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"changed"}

	_, err := NewRunner(runtime, &fakeStore{}).Run(context.Background(), RunRequest{
		Workspace:     runtimes.CreateRequest{RepositoryID: "dporkka/dev-plane"},
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err == nil || !strings.Contains(err.Error(), "gate changed failed") {
		t.Fatalf("Run() error = %v, want gate failure", err)
	}
	if len(runtime.destroyCalls) != 1 || runtime.destroyCalls[0] != "workspace-2" {
		t.Fatalf("destroy calls = %#v", runtime.destroyCalls)
	}
}

func TestRunnerReturnsCleanupFailureWithoutHidingVerificationFailure(t *testing.T) {
	runtime := &fakeWorkspaceRuntime{
		fakeRuntime: &fakeRuntime{
			config:        verifierConfig(),
			headResponses: []string{"head456", "head456"},
			results: map[string]*runtimes.CommandResult{
				"make verify-changed": {ExitCode: 1},
			},
			errors: map[string]error{},
		},
		createSession: &runtimes.Session{ID: "workspace-3", Status: "ready"},
		destroyErr:    errors.New("teardown failed"),
	}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"changed"}

	_, err := NewRunner(runtime, &fakeStore{}).Run(context.Background(), RunRequest{
		Workspace:     runtimes.CreateRequest{RepositoryID: "dporkka/dev-plane"},
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err == nil || !strings.Contains(err.Error(), "gate changed failed") || !strings.Contains(err.Error(), "teardown failed") {
		t.Fatalf("Run() error = %v, want verification and cleanup errors", err)
	}
}

func TestRunnerDoesNotDestroyWhenWorkspaceCreationFails(t *testing.T) {
	runtime := &fakeWorkspaceRuntime{
		fakeRuntime: &fakeRuntime{},
		createErr:   errors.New("capacity unavailable"),
	}

	_, err := NewRunner(runtime, &fakeStore{}).Run(context.Background(), RunRequest{
		Workspace:     runtimes.CreateRequest{RepositoryID: "dporkka/dev-plane"},
		WorkItem:      verifyingWorkItem(),
		CandidateHead: "head456",
	})
	if err == nil || !strings.Contains(err.Error(), "create verification workspace") {
		t.Fatalf("Run() error = %v, want create error", err)
	}
	if len(runtime.destroyCalls) != 0 {
		t.Fatalf("destroy calls = %#v, want none", runtime.destroyCalls)
	}
}
