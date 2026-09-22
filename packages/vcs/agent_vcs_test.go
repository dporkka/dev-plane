package vcs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func runRefineryGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func refineryRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runRefineryGit(t, repo, "init", "-b", "main")
	runRefineryGit(t, repo, "config", "user.name", "Dev Plane VCS Test")
	runRefineryGit(t, repo, "config", "user.email", "vcs-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRefineryGit(t, repo, "add", "README.md")
	runRefineryGit(t, repo, "commit", "-m", "base")
	return repo
}

func materializeTestCandidate(
	t *testing.T,
	repo, workspaceRoot, name, path, content string,
	recorder Recorder,
) VerifiedCandidate {
	t.Helper()
	workspace := filepath.Join(workspaceRoot, name)
	branch := "agent/" + name
	runRefineryGit(t, repo, "worktree", "add", "-b", branch, workspace, "main")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, path)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	candidate, err := NewCandidateMaterializer(nil, recorder).Materialize(
		context.Background(),
		workspace,
		"agent: materialize "+name,
		CandidateMetadata{
			TaskID:      "task-" + name,
			AgentID:     "agent-" + name,
			WorkspaceID: "workspace-" + name,
		},
	)
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	runRefineryGit(t, repo, "worktree", "remove", "--force", workspace)
	return candidate
}

func requireFiles(paths ...string) CandidateVerifier {
	return VerifyFunc(func(_ context.Context, workspace string) (VerificationReport, error) {
		evidence := make(map[string]string, len(paths))
		for _, path := range paths {
			if _, err := os.Stat(filepath.Join(workspace, path)); err != nil {
				evidence[path] = "missing"
				return VerificationReport{Passed: false, Evidence: evidence}, nil
			}
			evidence[path] = "present"
		}
		return VerificationReport{Passed: true, Evidence: evidence}, nil
	})
}

func TestCandidateMaterializerDisablesHooksAndRecordsProvenance(t *testing.T) {
	repo := refineryRepository(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 77\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	workspaceRoot := t.TempDir()
	workspace := filepath.Join(workspaceRoot, "candidate")
	runRefineryGit(t, repo, "worktree", "add", "-b", "agent/candidate", workspace, "main")
	if err := os.WriteFile(filepath.Join(workspace, "agent.txt"), []byte("verified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	recorder := &memoryRecorder{}
	candidate, err := NewCandidateMaterializer(nil, recorder).Materialize(
		context.Background(),
		workspace,
		"verified candidate",
		CandidateMetadata{TaskID: "task-1", AgentID: "agent-1", WorkspaceID: "ws-1"},
	)
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if !candidate.Changed {
		t.Fatal("Changed = false, want true")
	}
	if candidate.Branch != "agent/candidate" {
		t.Fatalf("Branch = %q", candidate.Branch)
	}
	if got := runRefineryGit(t, workspace, "rev-parse", "HEAD"); got != candidate.Revision.CommitID {
		t.Fatalf("HEAD = %q, candidate = %q", got, candidate.Revision.CommitID)
	}
	if got := runRefineryGit(t, workspace, "status", "--porcelain"); got != "" {
		t.Fatalf("workspace remains dirty: %q", got)
	}

	events := recorder.events
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Kind != EventKind("candidate.materialized") ||
		events[0].TaskID != "task-1" ||
		events[0].AgentID != "agent-1" ||
		events[0].CommitID != candidate.Revision.CommitID {
		t.Fatalf("unexpected provenance: %+v", events[0])
	}
}

func TestCandidateMaterializerCleanWorkspaceReusesHead(t *testing.T) {
	repo := refineryRepository(t)
	workspace := filepath.Join(t.TempDir(), "clean")
	runRefineryGit(t, repo, "worktree", "add", "-b", "agent/clean", workspace, "main")
	head := runRefineryGit(t, workspace, "rev-parse", "HEAD")

	candidate, err := NewCandidateMaterializer(nil, nil).Materialize(
		context.Background(),
		workspace,
		"verified clean candidate",
		CandidateMetadata{},
	)
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if candidate.Changed {
		t.Fatal("Changed = true, want false")
	}
	if candidate.Revision.CommitID != head {
		t.Fatalf("CommitID = %q, want %q", candidate.Revision.CommitID, head)
	}
}

func TestMergeRefineryMergesThenTreatsReplayAsAlreadyIntegrated(t *testing.T) {
	repo := refineryRepository(t)
	recorder := &memoryRecorder{}
	candidate := materializeTestCandidate(t, repo, t.TempDir(), "feature", "feature.txt", "feature\n", recorder)
	previous := runRefineryGit(t, repo, "rev-parse", "main")
	runRefineryGit(t, repo, "checkout", "--detach", "main")

	refinery, err := NewMergeRefinery(t.TempDir(), nil, recorder)
	if err != nil {
		t.Fatal(err)
	}
	request := MergeRequest{
		RepositoryPath: repo,
		TargetRef:      "main",
		Candidate:      candidate,
		Verifier:       requireFiles("feature.txt"),
	}
	outcome, err := refinery.Refine(context.Background(), request)
	if err != nil {
		t.Fatalf("Refine() error = %v", err)
	}
	if outcome.Status != MergeStatusMerged {
		t.Fatalf("Status = %q, want merged: %+v", outcome.Status, outcome)
	}
	if outcome.PreviousHead != previous || outcome.NewHead == "" || !outcome.Verification.Passed {
		t.Fatalf("unexpected merged outcome: %+v", outcome)
	}
	if got := runRefineryGit(t, repo, "show", "main:feature.txt"); got != "feature" {
		t.Fatalf("feature content = %q", got)
	}

	replay, err := refinery.Refine(context.Background(), request)
	if err != nil {
		t.Fatalf("replay Refine() error = %v", err)
	}
	if replay.Status != MergeStatusAlreadyIntegrated {
		t.Fatalf("replay Status = %q, want already_integrated", replay.Status)
	}
}

func TestMergeRefineryConflictDoesNotAdvanceTarget(t *testing.T) {
	repo := refineryRepository(t)
	candidate := materializeTestCandidate(t, repo, t.TempDir(), "conflict", "README.md", "candidate\n", nil)

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRefineryGit(t, repo, "add", "README.md")
	runRefineryGit(t, repo, "commit", "-m", "advance target")
	targetBefore := runRefineryGit(t, repo, "rev-parse", "main")
	runRefineryGit(t, repo, "checkout", "--detach", "main")

	refinery, err := NewMergeRefinery(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := refinery.Refine(context.Background(), MergeRequest{
		RepositoryPath: repo,
		TargetRef:      "main",
		Candidate:      candidate,
		Verifier:       requireFiles("README.md"),
	})
	if err != nil {
		t.Fatalf("Refine() error = %v", err)
	}
	if outcome.Status != MergeStatusConflict {
		t.Fatalf("Status = %q, want conflict: %+v", outcome.Status, outcome)
	}
	if got := runRefineryGit(t, repo, "rev-parse", "main"); got != targetBefore {
		t.Fatalf("main advanced from %q to %q", targetBefore, got)
	}
}

func TestMergeRefineryVerificationRejectionDoesNotAdvanceTarget(t *testing.T) {
	repo := refineryRepository(t)
	candidate := materializeTestCandidate(t, repo, t.TempDir(), "reject", "feature.txt", "feature\n", nil)
	targetBefore := runRefineryGit(t, repo, "rev-parse", "main")
	runRefineryGit(t, repo, "checkout", "--detach", "main")

	refinery, err := NewMergeRefinery(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := refinery.Refine(context.Background(), MergeRequest{
		RepositoryPath: repo,
		TargetRef:      "main",
		Candidate:      candidate,
		Verifier:       requireFiles("required.txt"),
	})
	if err != nil {
		t.Fatalf("Refine() error = %v", err)
	}
	if outcome.Status != MergeStatusRejected || outcome.Verification.Passed {
		t.Fatalf("unexpected rejection outcome: %+v", outcome)
	}
	if got := runRefineryGit(t, repo, "rev-parse", "main"); got != targetBefore {
		t.Fatalf("main advanced from %q to %q", targetBefore, got)
	}
}

func TestMergeRefineryRefusesCheckedOutTarget(t *testing.T) {
	repo := refineryRepository(t)
	candidate := materializeTestCandidate(t, repo, t.TempDir(), "checked-out", "feature.txt", "feature\n", nil)

	refinery, err := NewMergeRefinery(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = refinery.Refine(context.Background(), MergeRequest{
		RepositoryPath: repo,
		TargetRef:      "main",
		Candidate:      candidate,
		Verifier:       requireFiles("feature.txt"),
	})
	if !errors.Is(err, ErrTargetCheckedOut) {
		t.Fatalf("error = %v, want ErrTargetCheckedOut", err)
	}
}

func TestMergeRefineryCASProtectsExternalTargetWriter(t *testing.T) {
	repo := refineryRepository(t)
	root := t.TempDir()
	candidate := materializeTestCandidate(t, repo, root, "candidate-cas", "candidate.txt", "candidate\n", nil)

	externalWorkspace := filepath.Join(root, "external")
	runRefineryGit(t, repo, "worktree", "add", "-b", "external/writer", externalWorkspace, "main")
	if err := os.WriteFile(filepath.Join(externalWorkspace, "external.txt"), []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRefineryGit(t, externalWorkspace, "add", "external.txt")
	runRefineryGit(t, externalWorkspace, "commit", "-m", "external writer")
	externalCommit := runRefineryGit(t, externalWorkspace, "rev-parse", "HEAD")
	runRefineryGit(t, repo, "worktree", "remove", "--force", externalWorkspace)

	targetBefore := runRefineryGit(t, repo, "rev-parse", "main")
	runRefineryGit(t, repo, "checkout", "--detach", "main")

	var once sync.Once
	verifier := VerifyFunc(func(_ context.Context, workspace string) (VerificationReport, error) {
		if _, err := os.Stat(filepath.Join(workspace, "candidate.txt")); err != nil {
			return VerificationReport{Passed: false}, nil
		}
		once.Do(func() {
			runRefineryGit(t, repo, "update-ref", "refs/heads/main", externalCommit, targetBefore)
		})
		return VerificationReport{Passed: true}, nil
	})

	refinery, err := NewMergeRefinery(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := refinery.Refine(context.Background(), MergeRequest{
		RepositoryPath: repo,
		TargetRef:      "main",
		Candidate:      candidate,
		Verifier:       verifier,
	})
	if err != nil {
		t.Fatalf("Refine() error = %v", err)
	}
	if outcome.Status != MergeStatusStaleTarget {
		t.Fatalf("Status = %q, want stale_target: %+v", outcome.Status, outcome)
	}
	if outcome.PreviousHead != targetBefore || outcome.ActualHead != externalCommit {
		t.Fatalf("unexpected stale target outcome: %+v", outcome)
	}
	if got := runRefineryGit(t, repo, "rev-parse", "main"); got != externalCommit {
		t.Fatalf("external target writer was overwritten: main = %q, want %q", got, externalCommit)
	}
}

func TestMergeRefineryConcurrentCandidatesSerializeWithoutLostUpdates(t *testing.T) {
	repo := refineryRepository(t)
	root := t.TempDir()
	candidateA := materializeTestCandidate(t, repo, root, "a", "a.txt", "a\n", nil)
	candidateB := materializeTestCandidate(t, repo, root, "b", "b.txt", "b\n", nil)
	runRefineryGit(t, repo, "checkout", "--detach", "main")

	refinery, err := NewMergeRefinery(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestA := MergeRequest{
		RepositoryPath: repo,
		TargetRef:      "main",
		Candidate:      candidateA,
		Verifier:       requireFiles("a.txt"),
	}
	requestB := MergeRequest{
		RepositoryPath: repo,
		TargetRef:      "main",
		Candidate:      candidateB,
		Verifier:       requireFiles("b.txt"),
	}

	var wg sync.WaitGroup
	wg.Add(2)
	outcomes := make(chan MergeOutcome, 2)
	errs := make(chan error, 2)
	for _, request := range []MergeRequest{requestA, requestB} {
		request := request
		go func() {
			defer wg.Done()
			outcome, err := refinery.Refine(context.Background(), request)
			if err != nil {
				errs <- err
				return
			}
			outcomes <- outcome
		}()
	}
	wg.Wait()
	close(outcomes)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent Refine() error = %v", err)
	}
	count := 0
	for outcome := range outcomes {
		count++
		if outcome.Status != MergeStatusMerged {
			t.Fatalf("concurrent status = %q, want merged: %+v", outcome.Status, outcome)
		}
	}
	if count != 2 {
		t.Fatalf("outcomes = %d, want 2", count)
	}
	if got := runRefineryGit(t, repo, "show", "main:a.txt"); got != "a" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := runRefineryGit(t, repo, "show", "main:b.txt"); got != "b" {
		t.Fatalf("b.txt = %q", got)
	}
}


func TestReviewSnapshotIncludesUntrackedFilesAndMatchesCandidate(t *testing.T) {
	repo := refineryRepository(t)
	workspace := filepath.Join(t.TempDir(), "review-snapshot")
	runRefineryGit(t, repo, "worktree", "add", "-b", "agent/review-snapshot", workspace, "main")

	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := CaptureReviewSnapshot(context.Background(), nil, workspace)
	if err != nil {
		t.Fatalf("CaptureReviewSnapshot() error = %v", err)
	}
	if !strings.Contains(snapshot.Diff, "new.txt") {
		t.Fatalf("review patch omitted untracked file: %s", snapshot.Diff)
	}

	candidate, err := MaterializeReviewSnapshot(
		context.Background(),
		nil,
		workspace,
		"reviewed candidate",
		CandidateMetadata{TaskID: "task-review", AgentID: "run-review", WorkspaceID: "ws-review"},
		snapshot,
	)
	if err != nil {
		t.Fatalf("MaterializeReviewSnapshot() error = %v", err)
	}
	if !candidate.Changed {
		t.Fatal("candidate Changed = false, want true")
	}
	if got := runRefineryGit(t, workspace, "status", "--porcelain"); got != "" {
		t.Fatalf("workspace remains dirty after exact materialization: %q", got)
	}
}

func TestReviewSnapshotRejectsMutationAfterCaptureAndRestoresBase(t *testing.T) {
	repo := refineryRepository(t)
	workspace := filepath.Join(t.TempDir(), "review-race")
	runRefineryGit(t, repo, "worktree", "add", "-b", "agent/review-race", workspace, "main")

	path := filepath.Join(workspace, "README.md")
	if err := os.WriteFile(path, []byte("reviewed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CaptureReviewSnapshot(context.Background(), nil, workspace)
	if err != nil {
		t.Fatalf("CaptureReviewSnapshot() error = %v", err)
	}

	// Simulate a concurrent writer after the review snapshot was captured.
	if err := os.WriteFile(path, []byte("unreviewed mutation\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = MaterializeReviewSnapshot(
		context.Background(),
		nil,
		workspace,
		"raced candidate",
		CandidateMetadata{TaskID: "task-race", AgentID: "run-race", WorkspaceID: "ws-race"},
		snapshot,
	)
	if err == nil || !strings.Contains(err.Error(), "workspace changed") {
		t.Fatalf("error = %v, want workspace mutation rejection", err)
	}
	if got := runRefineryGit(t, workspace, "rev-parse", "HEAD"); got != snapshot.BaseCommit {
		t.Fatalf("HEAD = %q, want restored base %q", got, snapshot.BaseCommit)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "unreviewed mutation\n" {
		t.Fatalf("working tree mutation was lost: %q", data)
	}
}
