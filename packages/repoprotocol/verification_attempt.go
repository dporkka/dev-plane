package repoprotocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// VerificationGate describes one concluded check against an exact integration candidate.
type VerificationGate struct {
	Name       string     `json:"name"`
	Status     GateStatus `json:"status"`
	SubjectSHA string     `json:"subject_sha"`
	LogDigest  string     `json:"log_digest"`
}

// VerificationAttempt is a concluded, immutable verification event.
// Only trusted verifiers should produce these records; validation does not authenticate a producer.
type VerificationAttempt struct {
	ID                 string             `json:"id"`
	WorkItemID         string             `json:"work_item_id"`
	Repository         string             `json:"repository"`
	TargetBranch       string             `json:"target_branch"`
	SourceHeadSHA      string             `json:"source_head_sha"`
	TargetBaseSHA      string             `json:"target_base_sha"`
	CandidateCommitSHA string             `json:"candidate_commit_sha"`
	CandidateTreeSHA   string             `json:"candidate_tree_sha"`
	PolicyDigest       string             `json:"policy_digest"`
	RunnerDigest       string             `json:"runner_digest"`
	RequiredGates      []string           `json:"required_gates"`
	Gates              []VerificationGate `json:"gates"`
	CompletedAt        time.Time          `json:"completed_at"`
}

// Validate checks structural integrity, including failed attempts, so failures can be audited.
func (a VerificationAttempt) Validate() error {
	for field, value := range map[string]string{
		"id": a.ID, "work_item_id": a.WorkItemID, "repository": a.Repository,
		"target_branch": a.TargetBranch, "source_head_sha": a.SourceHeadSHA,
		"target_base_sha": a.TargetBaseSHA, "candidate_commit_sha": a.CandidateCommitSHA,
		"candidate_tree_sha": a.CandidateTreeSHA, "policy_digest": a.PolicyDigest,
		"runner_digest": a.RunnerDigest,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("verification attempt %s is required", field)
		}
	}
	if a.CompletedAt.IsZero() {
		return errors.New("verification attempt completed_at is required")
	}
	if len(a.RequiredGates) == 0 {
		return errors.New("verification attempt requires at least one required gate")
	}
	required := map[string]bool{}
	for _, name := range a.RequiredGates {
		name = strings.TrimSpace(name)
		if name == "" || required[name] {
			return fmt.Errorf("empty or duplicate required gate %q", name)
		}
		required[name] = true
	}
	if len(a.Gates) == 0 {
		return errors.New("verification attempt requires recorded gates")
	}
	seen := map[string]bool{}
	for _, gate := range a.Gates {
		name := strings.TrimSpace(gate.Name)
		if name == "" || seen[name] {
			return fmt.Errorf("empty or duplicate gate %q", name)
		}
		seen[name] = true
		if gate.SubjectSHA == "" || gate.LogDigest == "" {
			return fmt.Errorf("gate %q is missing subject or log digest", name)
		}
		switch gate.Status {
		case GatePassed, GateFailed, GateSkipped:
		default:
			return fmt.Errorf("gate %q has invalid or incomplete status %q", name, gate.Status)
		}
	}
	return nil
}

// Passed requires every recorded gate to have passed on the exact candidate.
// The union of recorded mandatory gates and the trusted caller's mandatory gates is enforced.
func (a VerificationAttempt) Passed(required []string) error {
	if err := a.Validate(); err != nil {
		return err
	}
	expected := make(map[string]bool, len(a.RequiredGates)+len(required))
	for _, name := range append(append([]string{}, a.RequiredGates...), required...) {
		if strings.TrimSpace(name) == "" {
			return errors.New("empty trusted required gate")
		}
		expected[name] = true
	}
	for _, gate := range a.Gates {
		if gate.SubjectSHA != a.CandidateCommitSHA {
			return fmt.Errorf("gate %q subject differs from integration candidate", gate.Name)
		}
		if gate.Status != GatePassed {
			return fmt.Errorf("gate %q is %s, not passed", gate.Name, gate.Status)
		}
		delete(expected, gate.Name)
	}
	for name := range expected {
		return fmt.Errorf("required gate %q has no passing result", name)
	}
	return nil
}
