package verifier

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

// integrityRuntime extends the existing verifier fake with Git's dirty-worktree
// responses. The underlying fake still records which verification gates ran.
type integrityRuntime struct {
	*fakeRuntime
	statusResponses []string
}

func (r *integrityRuntime) ExecuteCommand(ctx context.Context, sessionID string, cmd runtimes.Command) (*runtimes.CommandResult, error) {
	if reflect.DeepEqual(cmd.Args, []string{"git", "status", "--porcelain=v1", "--untracked-files=all"}) {
		r.commands = append(r.commands, cmd)
		if len(r.statusResponses) == 0 {
			return nil, errors.New("unexpected git status call")
		}
		status := r.statusResponses[0]
		r.statusResponses = r.statusResponses[1:]
		return &runtimes.CommandResult{Stdout: status, ExitCode: 0}, nil
	}
	return r.fakeRuntime.ExecuteCommand(ctx, sessionID, cmd)
}

func TestVerifyRejectsDirtyWorkspaceBeforeRunningAnyGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
	}{
		{name: "tracked modification", status: " M auth/session.go\n"},
		{name: "untracked file", status: "?? newly-generated-file.txt\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := &integrityRuntime{
				fakeRuntime: &fakeRuntime{
					config:        verifierConfig(),
					headResponses: []string{"head456"},
					results: map[string]*runtimes.CommandResult{
						"make verify-changed": {ExitCode: 0},
					},
				},
				statusResponses: []string{tc.status},
			}
			store := &fakeStore{}
			item := verifyingWorkItem()
			item.RequiredGates = []string{"changed"}

			_, err := New(runtime, store).Verify(context.Background(), Request{
				SessionID:     "session-1",
				WorkItem:      item,
				CandidateHead: "head456",
			})
			if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
				t.Fatalf("Verify() error = %v, want rejection of uncommitted changes", err)
			}
			if len(store.bundles) != 0 || len(store.workItems) != 0 {
				t.Fatalf("dirty worktree produced authoritative evidence or state: %#v %#v", store.bundles, store.workItems)
			}
			for _, cmd := range runtime.commands {
				if cmd.Command == "make verify-changed" {
					t.Fatal("verification gate executed against a dirty worktree")
				}
			}
		})
	}
}

func TestVerifyRejectsWorkspaceDirtiedByGate(t *testing.T) {
	runtime := &integrityRuntime{
		fakeRuntime: &fakeRuntime{
			config:        verifierConfig(),
			headResponses: []string{"head456", "head456"},
			results: map[string]*runtimes.CommandResult{
				"make verify-changed": {ExitCode: 0},
			},
		},
		statusResponses: []string{"", "?? test-report.json\n"},
	}
	store := &fakeStore{}
	item := verifyingWorkItem()
	item.RequiredGates = []string{"changed"}

	_, err := New(runtime, store).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("Verify() error = %v, want rejection of post-gate changes", err)
	}
	if len(store.bundles) != 0 || len(store.workItems) != 0 {
		t.Fatalf("post-gate changes produced authoritative evidence or state: %#v %#v", store.bundles, store.workItems)
	}
}

func TestVerifyRefusesToAdvanceWithoutExecutableVerification(t *testing.T) {
	runtime := &fakeRuntime{config: verifierConfig(), headResponses: []string{"head456"}}
	store := &fakeStore{}
	item := verifyingWorkItem()
	item.RequiredGates = []string{repoprotocol.GateIndependentReview}

	_, err := New(runtime, store).Verify(context.Background(), Request{
		SessionID:     "session-1",
		WorkItem:      item,
		CandidateHead: "head456",
	})
	if err == nil || !strings.Contains(err.Error(), "executable verification gate") {
		t.Fatalf("Verify() error = %v, want missing-executable-gate rejection", err)
	}
	if len(store.bundles) != 0 || len(store.workItems) != 0 {
		t.Fatalf("work advanced without executable verification: %#v %#v", store.bundles, store.workItems)
	}
}
