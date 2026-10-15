package repoprotocol

import (
	"strings"
	"testing"
	"time"
)

func validVerificationAttempt() VerificationAttempt {
	return VerificationAttempt{
		ID:                 "attempt-1",
		WorkItemID:         "work-1",
		Repository:         "dporkka/dev-plane",
		TargetBranch:       "main",
		SourceHeadSHA:      strings.Repeat("a", 40),
		TargetBaseSHA:      strings.Repeat("b", 40),
		CandidateCommitSHA: strings.Repeat("c", 40),
		CandidateTreeSHA:   strings.Repeat("d", 40),
		PolicyDigest:       "sha256:" + strings.Repeat("e", 64),
		RunnerDigest:       "sha256:" + strings.Repeat("f", 64),
		RequiredGates:      []string{"unit", "integration"},
		Gates: []VerificationGate{
			{Name: "unit", Status: GatePassed, SubjectSHA: strings.Repeat("c", 40), LogDigest: "sha256:" + strings.Repeat("1", 64)},
			{Name: "integration", Status: GatePassed, SubjectSHA: strings.Repeat("c", 40), LogDigest: "sha256:" + strings.Repeat("2", 64)},
		},
		CompletedAt: time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestVerificationAttemptPassesWithExactCandidateAndAllRequiredGates(t *testing.T) {
	attempt := validVerificationAttempt()
	if err := attempt.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := attempt.Passed([]string{"unit", "integration"}); err != nil {
		t.Fatal(err)
	}
}

func TestVerificationAttemptRejectsMissingGateWithoutTreatingSkippedAsPass(t *testing.T) {
	attempt := validVerificationAttempt()
	attempt.Gates = attempt.Gates[:1]
	if err := attempt.Passed([]string{"unit", "integration"}); err == nil {
		t.Fatal("absent integration gate must block")
	}
	attempt.Gates = validVerificationAttempt().Gates
	attempt.Gates[1].Status = GateSkipped
	if err := attempt.Passed([]string{"unit", "integration"}); err == nil {
		t.Fatal("skipped integration gate must block")
	}
}

func TestVerificationAttemptRejectsWrongGateSubjectAndDuplicateGates(t *testing.T) {
	attempt := validVerificationAttempt()
	attempt.Gates[0].SubjectSHA = strings.Repeat("a", 40)
	if err := attempt.Passed([]string{"unit"}); err == nil {
		t.Fatal("passing check on source instead of integration candidate must block")
	}
	attempt = validVerificationAttempt()
	attempt.Gates = append(attempt.Gates, attempt.Gates[0])
	if err := attempt.Validate(); err == nil {
		t.Fatal("duplicate check names must not be interpreted as valid evidence")
	}
}

func TestVerificationAttemptAllowsRecordingFailedEvidenceButNeverPromotesIt(t *testing.T) {
	attempt := validVerificationAttempt()
	attempt.Gates[0].Status = GateFailed
	if err := attempt.Validate(); err != nil {
		t.Fatalf("failed attempts must remain auditable: %v", err)
	}
	if err := attempt.Passed([]string{"unit", "integration"}); err == nil {
		t.Fatal("failed check must block")
	}
}

func TestVerificationAttemptRejectsMissingImmutableIdentities(t *testing.T) {
	attempt := validVerificationAttempt()
	attempt.PolicyDigest = ""
	if err := attempt.Validate(); err == nil {
		t.Fatal("policy digest required")
	}
	attempt = validVerificationAttempt()
	attempt.RunnerDigest = ""
	if err := attempt.Validate(); err == nil {
		t.Fatal("runner digest required")
	}
	attempt = validVerificationAttempt()
	attempt.CandidateTreeSHA = ""
	if err := attempt.Validate(); err == nil {
		t.Fatal("candidate tree required")
	}
}
