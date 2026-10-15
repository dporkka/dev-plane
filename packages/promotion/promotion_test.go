package promotion

import (
    "strings"
    "testing"
    "time"

    protocol "github.com/ai-dev-control-plane/repoprotocol"
)

func readyRequest() Request {
    source := strings.Repeat("a", 40)
    base := strings.Repeat("b", 40)
    candidate := strings.Repeat("c", 40)
    tree := strings.Repeat("d", 40)
    policy := "sha256:" + strings.Repeat("e", 64)
    attempt := protocol.VerificationAttempt{
        ID: "attempt-42", WorkItemID: "work-42", Repository: "dporkka/dev-plane",
        TargetBranch: "main", SourceHeadSHA: source, TargetBaseSHA: base,
        CandidateCommitSHA: candidate, CandidateTreeSHA: tree,
        PolicyDigest: policy, RunnerDigest: "sha256:" + strings.Repeat("f", 64),
        RequiredGates: []string{"unit", "integration"},
        Gates: []protocol.VerificationGate{
            {Name: "unit", Status: protocol.GatePassed, SubjectSHA: candidate, LogDigest: "sha256:" + strings.Repeat("1", 64)},
            {Name: "integration", Status: protocol.GatePassed, SubjectSHA: candidate, LogDigest: "sha256:" + strings.Repeat("2", 64)},
        },
        CompletedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
    }
    return Request{
        Snapshot: Snapshot{
            Repository: "dporkka/dev-plane", TargetBranch: "main",
            CurrentSourceHeadSHA: source, CurrentTargetHeadSHA: base,
            CandidateCommitSHA: candidate, CandidateTreeSHA: tree,
        },
        Policy: Policy{Digest: policy, RequiredGates: []string{"unit", "integration"}, RequiredHumanApprovals: 1},
        Attempt: attempt,
        AgentActorID: "agent-42",
        Approvals: []Approval{{ReviewerID: "human-1", Human: true, Approved: true, CandidateCommitSHA: candidate, PolicyDigest: policy}},
    }
}

func assertBlocked(t *testing.T, req Request, code BlockerCode) {
    t.Helper()
    result := Evaluate(req)
    if result.Eligible { t.Fatalf("unexpected eligibility: %+v", result) }
    for _, b := range result.Blockers {
        if b == code { return }
    }
    t.Fatalf("want %q blocker, got %+v", code, result.Blockers)
}

func TestEvaluateDryRunEligibleWhenCandidateChecksAndHumanApprovalMatch(t *testing.T) {
    result := Evaluate(readyRequest())
    if !result.Eligible || len(result.Blockers) != 0 {
        t.Fatalf("expected eligible dry run: %+v", result)
    }
}

func TestEvaluateBlocksSourceAndTargetDrift(t *testing.T) {
    req := readyRequest()
    req.Snapshot.CurrentSourceHeadSHA = strings.Repeat("9", 40)
    assertBlocked(t, req, BlockStaleSource)
    req = readyRequest()
    req.Snapshot.CurrentTargetHeadSHA = strings.Repeat("9", 40)
    assertBlocked(t, req, BlockStaleTarget)
}

func TestEvaluateBlocksCandidateAndPolicySubstitution(t *testing.T) {
    req := readyRequest()
    req.Snapshot.CandidateTreeSHA = strings.Repeat("9", 40)
    assertBlocked(t, req, BlockCandidateMismatch)
    req = readyRequest()
    req.Policy.Digest = "sha256:" + strings.Repeat("9", 64)
    assertBlocked(t, req, BlockPolicyMismatch)
}

func TestEvaluateBlocksMissingFailedOrUnrelatedChecks(t *testing.T) {
    req := readyRequest()
    req.Attempt.Gates = req.Attempt.Gates[:1]
    assertBlocked(t, req, BlockVerification)
    req = readyRequest()
    req.Attempt.Gates[0].Status = protocol.GateFailed
    assertBlocked(t, req, BlockVerification)
    req = readyRequest()
    req.Attempt.Gates[0].SubjectSHA = req.Attempt.SourceHeadSHA
    assertBlocked(t, req, BlockVerification)
}

func TestEvaluateRequiresDistinctIndependentCurrentHumanApprovals(t *testing.T) {
    req := readyRequest()
    req.Approvals[0].ReviewerID = req.AgentActorID
    assertBlocked(t, req, BlockApprovals)
    req = readyRequest()
    req.Approvals[0].CandidateCommitSHA = req.Attempt.SourceHeadSHA
    assertBlocked(t, req, BlockApprovals)
    req = readyRequest()
    req.Approvals[0].Human = false
    assertBlocked(t, req, BlockApprovals)
    req = readyRequest()
    req.Policy.RequiredHumanApprovals = 2
    req.Approvals = append(req.Approvals, req.Approvals[0])
    assertBlocked(t, req, BlockApprovals)
}

func TestEvaluateRejectsMissingPolicyOrCandidateIdentities(t *testing.T) {
    req := readyRequest()
    req.Snapshot.CurrentTargetHeadSHA = ""
    assertBlocked(t, req, BlockInvalidSnapshot)
    req = readyRequest()
    req.Attempt.RunnerDigest = ""
    assertBlocked(t, req, BlockInvalidEvidence)
}
