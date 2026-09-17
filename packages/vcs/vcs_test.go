package vcs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	commands []Command
	results  []CommandResult
	errs     []error
}

func (f *fakeRunner) Run(_ context.Context, cmd Command) (CommandResult, error) {
	f.commands = append(f.commands, cmd)
	index := len(f.commands) - 1
	var result CommandResult
	if index < len(f.results) {
		result = f.results[index]
	}
	var err error
	if index < len(f.errs) {
		err = f.errs[index]
	}
	return result, err
}

func TestMergeEnvDisablesTerminalPrompts(t *testing.T) {
	env := mergeEnv([]string{"PATH=/bin", "GIT_TERMINAL_PROMPT=1"}, map[string]string{"GIT_ASKPASS": "/tmp/askpass"})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	if !strings.Contains(joined, "\nGIT_TERMINAL_PROMPT=0\n") {
		t.Fatalf("terminal prompt was not disabled: %#v", env)
	}
	if !strings.Contains(joined, "\nGIT_ASKPASS=/tmp/askpass\n") {
		t.Fatalf("askpass override missing: %#v", env)
	}
}

func TestJujutsuCreateWorkspaceUsesExplicitBase(t *testing.T) {
	runner := &fakeRunner{}
	backend := NewJujutsuBackend(runner)
	req := WorkspaceRequest{
		RepositoryPath: t.TempDir(),
		WorkspacePath:  filepath.Join(t.TempDir(), "agent-task-42"),
		Name:           "agent-task-42",
		Base:           "main@origin",
	}
	if err := backend.CreateWorkspace(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	want := []string{"workspace", "add", "--name", "agent-task-42", "-r", "main@origin", req.WorkspacePath}
	if got := runner.commands[0].Args; !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestJujutsuSnapshotReturnsStableChangeID(t *testing.T) {
	runner := &fakeRunner{results: []CommandResult{
		{Stdout: "M src/main.go\n"},
		{},
		{Stdout: "0123456789abcdef|abcdef0123456789\n"},
	}}
	backend := NewJujutsuBackend(runner)
	revision, err := backend.Snapshot(context.Background(), t.TempDir(), "feat: agent change")
	if err != nil {
		t.Fatal(err)
	}
	if revision.CommitID != "0123456789abcdef" || revision.ChangeID != "abcdef0123456789" {
		t.Fatalf("unexpected revision: %+v", revision)
	}
	if got := runner.commands[1].Args; !reflect.DeepEqual(got, []string{"commit", "-m", "feat: agent change"}) {
		t.Fatalf("commit args = %#v", got)
	}
	if got := runner.commands[2].Args[2]; got != "@-" {
		t.Fatalf("revision queried %q, want @-", got)
	}
}

func TestJujutsuPublishUsesBookmarkAndLeaseSafePush(t *testing.T) {
	runner := &fakeRunner{}
	backend := NewJujutsuBackend(runner)
	if err := backend.Publish(context.Background(), PublishRequest{WorkspacePath: t.TempDir(), Ref: "agent/task-42", Env: map[string]string{"GIT_ASKPASS": "/tmp/askpass"}}); err != nil {
		t.Fatal(err)
	}
	wantSet := []string{"bookmark", "set", "--allow-backwards", "agent/task-42", "-r", "@-"}
	wantPush := []string{"git", "push", "--remote", "origin", "--bookmark", "agent/task-42"}
	if !reflect.DeepEqual(runner.commands[0].Args, wantSet) {
		t.Fatalf("bookmark args = %#v", runner.commands[0].Args)
	}
	if !reflect.DeepEqual(runner.commands[1].Args, wantPush) {
		t.Fatalf("push args = %#v", runner.commands[1].Args)
	}
	if got := runner.commands[1].Env["GIT_ASKPASS"]; got != "/tmp/askpass" {
		t.Fatalf("push auth env = %q", got)
	}
}

func TestCloneRejectsCredentialBearingURL(t *testing.T) {
	runner := &fakeRunner{}
	backend := NewGitBackend(runner)
	err := backend.CloneOrFetch(context.Background(), CloneRequest{
		URL: "https://secret@example.com/org/repo.git", Path: filepath.Join(t.TempDir(), "repo"),
	})
	if err == nil || !strings.Contains(err.Error(), "must not contain credentials") {
		t.Fatalf("expected credential rejection, got %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("runner should not be invoked for invalid URL")
	}
}

func TestJSONLRecorderPersistsOneEventPerLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provenance", "events.jsonl")
	recorder := NewJSONLRecorder(path)
	event := ProvenanceEvent{
		ID: "event-1", Timestamp: time.Unix(123, 0).UTC(), Kind: EventSnapshotCreated,
		Backend: "jj", TaskID: "task-42", AgentID: "coder-7", Workspace: "agent-task-42",
	}
	if err := recorder.Record(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	var decoded ProvenanceEvent
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != "event-1" || decoded.TaskID != "task-42" || decoded.Backend != "jj" {
		t.Fatalf("unexpected event: %+v", decoded)
	}
}

func TestJujutsuCloneForcesColocation(t *testing.T) {
	runner := &fakeRunner{}
	backend := NewJujutsuBackend(runner)
	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := backend.CloneOrFetch(context.Background(), CloneRequest{
		URL: "https://example.com/acme/repo.git", Path: repoPath,
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"git", "clone", "--colocate", "https://example.com/acme/repo.git", repoPath}
	if !reflect.DeepEqual(runner.commands[0].Args, want) {
		t.Fatalf("clone args = %#v, want %#v", runner.commands[0].Args, want)
	}
}

func TestNewBackend(t *testing.T) {
	backend, err := NewBackend("jujutsu", &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if backend.Name() != "jj" {
		t.Fatalf("backend = %q, want jj", backend.Name())
	}
	if _, err := NewBackend("svn", &fakeRunner{}); err == nil {
		t.Fatal("expected unsupported backend error")
	}
}

type fakeBackend struct {
	snapshot Revision
}

func (fakeBackend) Name() string                                            { return "fake" }
func (fakeBackend) CloneOrFetch(context.Context, CloneRequest) error        { return nil }
func (fakeBackend) CreateWorkspace(context.Context, WorkspaceRequest) error { return nil }
func (fakeBackend) RemoveWorkspace(context.Context, WorkspaceRequest) error { return nil }
func (fakeBackend) Status(context.Context, string) (string, error)          { return "", nil }
func (fakeBackend) Diff(context.Context, string) (string, error)            { return "", nil }
func (b fakeBackend) Snapshot(context.Context, string, string) (Revision, error) {
	return b.snapshot, nil
}
func (fakeBackend) Publish(context.Context, PublishRequest) error { return nil }

type memoryRecorder struct{ events []ProvenanceEvent }

func (r *memoryRecorder) Record(_ context.Context, event ProvenanceEvent) error {
	r.events = append(r.events, event)
	return nil
}

func TestManagerCarriesTaskAndAgentProvenance(t *testing.T) {
	recorder := &memoryRecorder{}
	manager, err := NewManager(fakeBackend{snapshot: Revision{CommitID: "c1", ChangeID: "change1"}}, recorder)
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return time.Unix(456, 0).UTC() }
	workspace, err := manager.Prepare(context.Background(), PrepareRequest{
		TaskID: "task-42", AgentID: "coder-7", RepositoryURL: "https://example.com/a/repo.git",
		RepositoryPath: "/cache/repo", WorkspacePath: "/work/task-42", WorkspaceName: "task-42",
		Base: "main@origin", PublishRef: "agent/task-42",
	})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := manager.Snapshot(context.Background(), workspace, "feat: implement task")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Publish(context.Background(), workspace, revision); err != nil {
		t.Fatal(err)
	}
	if len(recorder.events) != 3 {
		t.Fatalf("events = %d, want 3", len(recorder.events))
	}
	last := recorder.events[2]
	if last.Kind != EventPublished || last.TaskID != "task-42" || last.AgentID != "coder-7" || last.ChangeID != "change1" {
		t.Fatalf("unexpected published provenance: %+v", last)
	}
}
