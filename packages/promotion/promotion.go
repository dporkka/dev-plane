// Package promotion evaluates eligibility for promotion without mutating Git or the hosting provider.
package promotion

import (
    "strings"

    protocol "github.com/ai-dev-control-plane/repoprotocol"
)

type BlockerCode string

const (
    BlockInvalidSnapshot  BlockerCode = "invalid_snapshot"
    BlockInvalidPolicy    BlockerCode = "invalid_policy"
    BlockInvalidEvidence  BlockerCode = "invalid_evidence"
    BlockStaleSource      BlockerCode = "stale_source"
    BlockStaleTarget      BlockerCode = "stale_target"
    BlockCandidateMismatch BlockerCode = "candidate_mismatch"
    BlockPolicyMismatch   BlockerCode = "policy_mismatch"
    BlockVerification     BlockerCode = "verification_incomplete"
    BlockApprovals        BlockerCode = "approvals_incomplete"
)

// Snapshot must be assembled by a trusted provider adapter from currently observed remote refs.
// It is a point-in-time observation, NOT a lease or merge authorization.
type Snapshot struct {
    Repository string
    TargetBranch string
    CurrentSourceHeadSHA string
    CurrentTargetHeadSHA string
    CandidateCommitSHA string
    CandidateTreeSHA string
}

// Policy must be loaded from a trusted protected revision, not the candidate's working tree.
type Policy struct {
    Digest string
    RequiredGates []string
    RequiredHumanApprovals int
}

// Approval is evidence from an independently authenticated reviewer.
// Caller must verify reviewer identity and the source of this record.
type Approval struct {
    ReviewerID string
    Human bool
    Approved bool
    CandidateCommitSHA string
    PolicyDigest string
}

type Request struct {
    Snapshot Snapshot
    Policy Policy
    Attempt protocol.VerificationAttempt
    AgentActorID string
    Approvals []Approval
}

type Decision struct {
    Eligible bool `json:"eligible"`
    DryRun bool `json:"dry_run"`
    AttemptID string `json:"attempt_id"`
    Blockers []BlockerCode `json:"blockers,omitempty"`
}

// Evaluate is deliberately pure and fail-closed. Eligible means only "no blockers
// for this supplied snapshot"; the caller must re-check remote refs and merge authority.
func Evaluate(req Request) Decision {
    decision := Decision{DryRun: true, AttemptID: req.Attempt.ID}
    block := func(code BlockerCode) {
        for _, old := range decision.Blockers {
            if old == code { return }
        }
        decision.Blockers = append(decision.Blockers, code)
    }
    s, p, a := req.Snapshot, req.Policy, req.Attempt
    if anyBlank(s.Repository, s.TargetBranch, s.CurrentSourceHeadSHA,
        s.CurrentTargetHeadSHA, s.CandidateCommitSHA, s.CandidateTreeSHA) {
        block(BlockInvalidSnapshot)
    }
    if anyBlank(p.Digest) || len(p.RequiredGates) == 0 || p.RequiredHumanApprovals < 0 {
        block(BlockInvalidPolicy)
    }
    for _, check := range p.RequiredGates {
        if strings.TrimSpace(check) == "" { block(BlockInvalidPolicy) }
    }
    if err := a.Validate(); err != nil {
        block(BlockInvalidEvidence)
    }
    if s.Repository != a.Repository || s.TargetBranch != a.TargetBranch ||
        s.CandidateCommitSHA != a.CandidateCommitSHA || s.CandidateTreeSHA != a.CandidateTreeSHA {
        block(BlockCandidateMismatch)
    }
    if s.CurrentSourceHeadSHA != a.SourceHeadSHA { block(BlockStaleSource) }
    if s.CurrentTargetHeadSHA != a.TargetBaseSHA { block(BlockStaleTarget) }
    if p.Digest != a.PolicyDigest { block(BlockPolicyMismatch) }
    if err := a.Passed(p.RequiredGates); err != nil { block(BlockVerification) }

    seen := make(map[string]bool)
    humanCount := 0
    for _, approval := range req.Approvals {
        id := strings.TrimSpace(approval.ReviewerID)
        if id == "" || id == req.AgentActorID || seen[id] || !approval.Human || !approval.Approved ||
            approval.CandidateCommitSHA != a.CandidateCommitSHA || approval.PolicyDigest != p.Digest {
            continue
        }
        seen[id] = true
        humanCount++
    }
    if humanCount < p.RequiredHumanApprovals { block(BlockApprovals) }
    decision.Eligible = len(decision.Blockers) == 0
    return decision
}

func anyBlank(values ...string) bool {
    for _, value := range values {
        if strings.TrimSpace(value) == "" { return true }
    }
    return false
}
