package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/db"
	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

type completionTestRuntime struct {
	*fakeRuntimeProvider
	head        string
	dirty       bool
	gateFailure bool
	staleAfter  bool
	commands    []runtimes.Command
}

func (r *completionTestRuntime) ReadFile(_ context.Context, _, path string) ([]byte, error) {
	if path != "devplane.yaml" {
		return nil, errors.New("unexpected verification file")
	}
	return []byte("version: 1\nverification:\n  changed:\n    command: printf ok\nwork:\n  isolation: worktree\n  max_parallel_cost: 1\n"), nil
}

func (r *completionTestRuntime) ExecuteCommand(_ context.Context, _ string, command runtimes.Command) (*runtimes.CommandResult, error) {
	r.commands = append(r.commands, command)
	if command.Command == "printf ok" {
		if r.gateFailure {
			return &runtimes.CommandResult{ExitCode: 1, Stderr: "focused test failed"}, nil
		}
		return &runtimes.CommandResult{ExitCode: 0, Stdout: "ok"}, nil
	}
	if len(command.Args) == 0 || command.Args[0] != "git" {
		return nil, errors.New("unexpected command")
	}
	switch {
	case len(command.Args) >= 3 && command.Args[1] == "rev-parse":
		head := r.head
		if r.staleAfter && len(r.commands) > 6 {
			head = strings.Repeat("c", 40)
		}
		return &runtimes.CommandResult{Stdout: head + "\n"}, nil
	case len(command.Args) > 1 && command.Args[1] == "merge-base":
		return &runtimes.CommandResult{ExitCode: 0}, nil
	case len(command.Args) > 1 && command.Args[1] == "diff":
		return &runtimes.CommandResult{Stdout: "src/change.go\x00"}, nil
	case len(command.Args) > 1 && command.Args[1] == "status":
		if r.dirty {
			return &runtimes.CommandResult{Stdout: "?? untracked.txt\n"}, nil
		}
		return &runtimes.CommandResult{}, nil
	default:
		return nil, errors.New("unexpected Git operation")
	}
}

func setupCompletionAuthority(t *testing.T) (*db.DB, repoprotocol.WorkItem) {
	t.Helper()
	raw := setupRunHandlerDB(t)
	t.Cleanup(func() { _ = raw.Close() })
	insertCompletedRunFixture(t, raw, "implementer")
	base := strings.Repeat("a", 40)
	settings, _ := json.Marshal(map[string]string{"base_sha": base})
	_, err := raw.Exec(`
		CREATE TABLE workspaces (id TEXT PRIMARY KEY, task_id TEXT, runtime_session_id TEXT, settings TEXT);
		CREATE TABLE work_items (id TEXT PRIMARY KEY, repository TEXT, state TEXT, claimed_by TEXT, lease_until DATETIME, payload TEXT, updated_at DATETIME);
		CREATE TABLE evidence_bundles (work_item_id TEXT, head_sha TEXT, base_sha TEXT, payload TEXT, updated_at DATETIME, PRIMARY KEY(work_item_id, head_sha));
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec("INSERT INTO workspaces (id, task_id, runtime_session_id, settings) VALUES (?, ?, ?, ?)",
		"workspace-1", "task-1", "session-1", string(settings))
	if err != nil {
		t.Fatal(err)
	}
	work := repoprotocol.WorkItem{
		ID: "task-1", Repository: "example/repo", Objective: "Fix exact SHA candidate", BaseSHA: base,
		OwnershipPaths: []string{"src/change.go"}, RequiredGates: []string{"changed"},
		State: repoprotocol.WorkImplementing, Risk: repoprotocol.RiskLow, Cost: 1,
	}
	store := &db.DB{DB: raw, Driver: "sqlite"}
	if err := store.PutWorkItem(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	return store, work
}

func TestRepositoryCompletionVerifierUsesPersistedBaseAndTestEvidence(t *testing.T) {
	store, work := setupCompletionAuthority(t)
	rt := &completionTestRuntime{fakeRuntimeProvider: &fakeRuntimeProvider{}, head: strings.Repeat("b", 40)}
	out, err := NewRepositoryCompletionVerifier(store, rt).VerifyCompletedRun(context.Background(), "run-1", "task-1")
	if err != nil {
		t.Fatalf("VerifyCompletedRun: %v", err)
	}
	if out.BaseSHA != work.BaseSHA || out.HeadSHA != rt.head {
		t.Fatalf("receipt=%+v, expected base=%s head=%s", out, work.BaseSHA, rt.head)
	}
	evidence, err := store.GetEvidenceBundle(context.Background(), work.ID, rt.head)
	if err != nil {
		t.Fatalf("persisted evidence: %v", err)
	}
	if err := evidence.ValidateHead(rt.head); err != nil {
		t.Fatalf("evidence not valid: %v", err)
	}
	if evidence.BaseSHA != work.BaseSHA {
		t.Fatalf("evidence base is %s", evidence.BaseSHA)
	}
	stored, err := store.GetWorkItem(context.Background(), work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != repoprotocol.WorkReadyToLand {
		t.Fatalf("work state=%s", stored.State)
	}
}

func TestRepositoryCompletionVerifierFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *db.DB, *completionTestRuntime, repoprotocol.WorkItem)
	}{
		{name: "missing work item", prepare: func(t *testing.T, store *db.DB, _ *completionTestRuntime, _ repoprotocol.WorkItem) {
			if _, err := store.Exec("DELETE FROM work_items WHERE id='task-1'"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "untrusted base revision", prepare: func(t *testing.T, store *db.DB, _ *completionTestRuntime, w repoprotocol.WorkItem) {
			w.BaseSHA = strings.Repeat("f", 40)
			if err := store.PutWorkItem(context.Background(), w); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "no executable gates", prepare: func(t *testing.T, store *db.DB, _ *completionTestRuntime, w repoprotocol.WorkItem) {
			w.RequiredGates = []string{repoprotocol.GateHumanApproval}
			if err := store.PutWorkItem(context.Background(), w); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "dirty worktree", prepare: func(_ *testing.T, _ *db.DB, rt *completionTestRuntime, _ repoprotocol.WorkItem) { rt.dirty = true }},
		{name: "failed focused gate", prepare: func(_ *testing.T, _ *db.DB, rt *completionTestRuntime, _ repoprotocol.WorkItem) {
			rt.gateFailure = true
		}},
		{name: "completed run status not trusted", prepare: func(t *testing.T, store *db.DB, _ *completionTestRuntime, _ repoprotocol.WorkItem) {
			if _, err := store.Exec("UPDATE agent_runs SET status='running' WHERE id='run-1'"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing session", prepare: func(t *testing.T, store *db.DB, _ *completionTestRuntime, _ repoprotocol.WorkItem) {
			if _, err := store.Exec("UPDATE workspaces SET runtime_session_id=NULL WHERE id='workspace-1'"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, work := setupCompletionAuthority(t)
			rt := &completionTestRuntime{fakeRuntimeProvider: &fakeRuntimeProvider{}, head: strings.Repeat("b", 40)}
			tc.prepare(t, store, rt, work)
			if receipt, err := NewRepositoryCompletionVerifier(store, rt).VerifyCompletedRun(context.Background(), "run-1", "task-1"); err == nil {
				t.Fatalf("failed-open completion receipt %+v", receipt)
			}
		})
	}
}

func TestRepositoryCompletionVerifierRejectsCrossTaskEvent(t *testing.T) {
	store, _ := setupCompletionAuthority(t)
	rt := &completionTestRuntime{fakeRuntimeProvider: &fakeRuntimeProvider{}, head: strings.Repeat("b", 40)}
	if _, err := NewRepositoryCompletionVerifier(store, rt).VerifyCompletedRun(context.Background(), "run-1", "other-task"); err == nil {
		t.Fatal("cross-task run notification was accepted")
	}
}
