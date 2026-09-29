package vcs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeRefineryMaterializeAndIntegrate(t *testing.T) {
	tempDir := t.TempDir()
	runner := ExecRunner{}
	backend := NewGitBackend(runner)
	provPath := filepath.Join(tempDir, "provenance.jsonl")
	recorder := NewJSONLRecorder(provPath)

	manager, err := NewManager(backend, recorder)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Create a real local git repository for hermetic offline testing
	repoPath := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repoPath failed: %v", err)
	}

	if _, err := runner.Run(context.Background(), Command{Name: "git", Args: []string{"init"}, Dir: repoPath}); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	if _, err := runner.Run(context.Background(), Command{Name: "git", Args: []string{"config", "user.name", "Test Agent"}, Dir: repoPath}); err != nil {
		t.Fatalf("git config name failed: %v", err)
	}
	if _, err := runner.Run(context.Background(), Command{Name: "git", Args: []string{"config", "user.email", "agent@test.local"}, Dir: repoPath}); err != nil {
		t.Fatalf("git config email failed: %v", err)
	}

	testFile := filepath.Join(repoPath, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test Repo\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	if _, err := runner.Run(context.Background(), Command{Name: "git", Args: []string{"add", "."}, Dir: repoPath}); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if _, err := runner.Run(context.Background(), Command{Name: "git", Args: []string{"commit", "-m", "initial commit"}, Dir: repoPath}); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
	if _, err := runner.Run(context.Background(), Command{Name: "git", Args: []string{"branch", "-M", "main"}, Dir: repoPath}); err != nil {
		t.Fatalf("git branch failed: %v", err)
	}

	wsPath := filepath.Join(tempDir, "ws1")
	prepReq := PrepareRequest{
		TaskID:         "task-101",
		AgentID:        "agent-alpha",
		RepositoryURL:  repoPath,
		RepositoryPath: filepath.Join(tempDir, "local_cache"),
		WorkspacePath:  wsPath,
		WorkspaceName:  "task-101-ws",
		Base:           "main",
		PublishRef:     "feature-101",
	}

	workspace, err := manager.Prepare(context.Background(), prepReq)
	if err != nil {
		t.Fatalf("Prepare workspace failed: %v", err)
	}

	refinery, err := NewMergeRefinery(manager)
	if err != nil {
		t.Fatalf("NewMergeRefinery failed: %v", err)
	}

	// 1. Materialize Candidate
	// Add a change to workspace
	if err := os.WriteFile(filepath.Join(wsPath, "FEATURE.md"), []byte("new feature\n"), 0o644); err != nil {
		t.Fatalf("write FEATURE.md failed: %v", err)
	}

	candidate, err := refinery.MaterializeCandidate(context.Background(), workspace, "feat: verified candidate implementation")
	if err != nil {
		t.Fatalf("MaterializeCandidate failed: %v", err)
	}

	if candidate.ID == "" {
		t.Errorf("expected candidate ID to be set")
	}
	if candidate.TaskID != "task-101" || candidate.AgentID != "agent-alpha" {
		t.Errorf("candidate identity mismatch: got task=%s agent=%s", candidate.TaskID, candidate.AgentID)
	}

	// 2. Successful Integration with Verifier
	verifierCalled := false
	verifier := func(ctx context.Context, targetPath string) error {
		verifierCalled = true
		return nil
	}

	res, err := refinery.Integrate(context.Background(), candidate, verifier, "")
	if err != nil {
		t.Fatalf("Integrate failed: %v", err)
	}

	if res.Status != OutcomeSuccess {
		t.Errorf("expected OutcomeSuccess, got %s (err: %s)", res.Status, res.ErrorMessage)
	}
	if !verifierCalled {
		t.Errorf("expected verifier callback to be called")
	}

	// 3. Idempotency Check: repeated delivery returns previous result
	res2, err := refinery.Integrate(context.Background(), candidate, verifier, "")
	if err != nil {
		t.Fatalf("Integrate repeated delivery failed: %v", err)
	}
	if res2.CandidateID != res.CandidateID || res2.Status != OutcomeSuccess {
		t.Errorf("idempotency result mismatch: got status=%s", res2.Status)
	}

	// 4. CAS Stale Target Check
	candidateCAS := candidate
	candidateCAS.ID = "cand-cas-test"
	candidateCAS.ExpectedTarget = "commit-abc1234"

	resCAS, err := refinery.Integrate(context.Background(), candidateCAS, nil, "commit-xyz9876")
	if err != nil {
		t.Fatalf("Integrate CAS test failed: %v", err)
	}
	if resCAS.Status != OutcomeStaleTarget {
		t.Errorf("expected OutcomeStaleTarget, got %s", resCAS.Status)
	}

	// 5. Deterministic Re-verification Failure
	candidateReject := candidate
	candidateReject.ID = "cand-reject-test"
	candidateReject.ExpectedTarget = ""

	rejectVerifier := func(ctx context.Context, targetPath string) error {
		return errors.New("unit test failure on replayed target state")
	}

	resReject, err := refinery.Integrate(context.Background(), candidateReject, rejectVerifier, "")
	if err != nil {
		t.Fatalf("Integrate rejection test failed: %v", err)
	}
	if resReject.Status != OutcomeRejection {
		t.Errorf("expected OutcomeRejection, got %s", resReject.Status)
	}
}

func TestDAGBarrierSemantics(t *testing.T) {
	nodeA := &DAGNode{ID: "node-A", TaskID: "task-A", State: DAGPending}
	nodeB := &DAGNode{ID: "node-B", TaskID: "task-B", Dependencies: []string{"node-A"}, State: DAGPending}
	nodeC := &DAGNode{ID: "node-C", TaskID: "task-C", Dependencies: []string{"node-B"}, State: DAGPending}

	barrier := NewDAGBarrier([]*DAGNode{nodeA, nodeB, nodeC})

	// 1. node-B should not be ready initially because node-A is PENDING
	readyB, err := barrier.IsReady("node-B")
	if err != nil {
		t.Fatalf("IsReady node-B failed: %v", err)
	}
	if readyB {
		t.Errorf("expected node-B not to be ready while node-A is PENDING")
	}

	// 2. Mark node-A integrated
	if err := barrier.MarkIntegrated("node-A", "refs/heads/main@commit-A"); err != nil {
		t.Fatalf("MarkIntegrated node-A failed: %v", err)
	}

	// 3. node-B should now be ready
	readyB2, err := barrier.IsReady("node-B")
	if err != nil {
		t.Fatalf("IsReady node-B after integration failed: %v", err)
	}
	if !readyB2 {
		t.Errorf("expected node-B to be ready after node-A achieved DAGIntegrated")
	}

	// 4. node-C should still not be ready
	readyC, err := barrier.IsReady("node-C")
	if err != nil {
		t.Fatalf("IsReady node-C failed: %v", err)
	}
	if readyC {
		t.Errorf("expected node-C not to be ready while node-B is PENDING")
	}
}
