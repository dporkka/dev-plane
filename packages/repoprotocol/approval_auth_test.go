package repoprotocol

import (
	"fmt"
	"testing"
)

func TestApprovalRequiresVerifier(t *testing.T) {
	b := EvidenceBundle{WorkItemID: "work-1", BaseSHA: "base", HeadSHA: "head", Gates: []GateEvidence{{Name: GateHumanApproval, Status: GatePassed}}}
	if err := b.ValidateHead("head"); err == nil {
		t.Fatal("self-asserted approval accepted")
	}
}

type testApprovalVerifier func(string, string, string) error

func (f testApprovalVerifier) VerifyApproval(workItem, gate, candidate string) error {
	return f(workItem, gate, candidate)
}

func TestTrustedReviewVerifierMustCheckBothApprovalGates(t *testing.T) {
	b := EvidenceBundle{WorkItemID: "work-1", BaseSHA: "base", HeadSHA: "head", Gates: []GateEvidence{
		{Name: GateIndependentReview, Status: GatePassed},
		{Name: GateHumanApproval, Status: GatePassed},
	}}
	seen := map[string]bool{}
	v := testApprovalVerifier(func(item, gate, head string) error {
		if head != "head" {
			return fmt.Errorf("wrong head")
		}
		seen[gate] = true
		return nil
	})
	if err := b.ValidateHeadWithAuthorities("head", nil, v); err != nil {
		t.Fatal(err)
	}
	if !seen[GateIndependentReview] || !seen[GateHumanApproval] {
		t.Fatalf("missing checks %+v", seen)
	}
	if err := b.ValidateHeadWithAuthorities("wrong", nil, v); err == nil {
		t.Fatal("wrong head accepted")
	}
	if err := b.ValidateHeadWithAuthorities("head", nil, nil); err == nil {
		t.Fatal("missing verifier accepted")
	}
}

func TestRejectionOfOneApprovalBlocksRelease(t *testing.T) {
	b := EvidenceBundle{WorkItemID: "work-1", BaseSHA: "base", HeadSHA: "head", Gates: []GateEvidence{
		{Name: GateIndependentReview, Status: GatePassed},
		{Name: GateHumanApproval, Status: GatePassed},
	}}
	v := testApprovalVerifier(func(item, gate, head string) error {
		if gate == GateHumanApproval {
			return fmt.Errorf("not authorized")
		}
		return nil
	})
	if err := b.ValidateHeadWithAuthorities("head", nil, v); err == nil {
		t.Fatal("denied approval accepted")
	}
}

func TestApprovalVerifierChecksWorkItemScope(t *testing.T) {
	b := EvidenceBundle{WorkItemID: "item-A", BaseSHA: "base", HeadSHA: "head",
		Gates: []GateEvidence{{Name: GateHumanApproval, Status: GatePassed}}}
	auth := testApprovalVerifier(func(item, gate, head string) error {
		if item != "item-A" || gate != GateHumanApproval || head != "head" {
			return fmt.Errorf("wrong target")
		}
		return nil
	})
	if err := b.ValidateHeadWithAuthorities("head", nil, auth); err != nil {
		t.Fatal(err)
	}
	b.WorkItemID = "item-B"
	if err := b.ValidateHeadWithAuthorities("head", nil, auth); err == nil {
		t.Fatal("other work item reused approval")
	}
	b.WorkItemID = ""
	if err := b.ValidateHeadWithAuthorities("head", nil, auth); err == nil {
		t.Fatal("missing work item accepted")
	}
	b.WorkItemID = "item-A"
	if err := b.ValidateHeadWithRemoteCI("head", nil); err == nil {
		t.Fatal("remote policy alone accepted human approval")
	}
}
