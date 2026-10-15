package repoprotocol

import (
	"strings"
	"testing"
)

func TestVerificationAttemptSignatureBindsEveryPromotableField(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	attempt := validVerificationAttempt()
	signed, err := SignVerificationAttempt(attempt, key)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Signature == "" {
		t.Fatal("missing signature")
	}
	if err := VerifyVerificationAttemptSignature(signed, key); err != nil {
		t.Fatalf("signature verification: %v", err)
	}

	cases := map[string]func(*VerificationAttempt){
		"candidate tree": func(a *VerificationAttempt) { a.CandidateTreeSHA = strings.Repeat("9", 40) },
		"target base":    func(a *VerificationAttempt) { a.TargetBaseSHA = strings.Repeat("9", 40) },
		"policy":         func(a *VerificationAttempt) { a.PolicyDigest = "sha256:" + strings.Repeat("9", 64) },
		"runner":         func(a *VerificationAttempt) { a.RunnerDigest = "sha256:" + strings.Repeat("9", 64) },
		"check":          func(a *VerificationAttempt) { a.Gates[0].LogDigest = "sha256:" + strings.Repeat("9", 64) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			altered := signed
			altered.Gates = append([]VerificationGate(nil), signed.Gates...)
			mutate(&altered)
			if err := VerifyVerificationAttemptSignature(altered, key); err == nil {
				t.Fatal("tampered attempt accepted")
			}
		})
	}
}

func TestVerificationAttemptSignatureFailClosedOnMissingOrWeakKey(t *testing.T) {
	attempt := validVerificationAttempt()
	key := []byte(strings.Repeat("s", 32))
	if err := VerifyVerificationAttemptSignature(attempt, key); err == nil {
		t.Fatal("unsigned attempt accepted")
	}
	if _, err := SignVerificationAttempt(attempt, []byte("test")); err == nil {
		t.Fatal("weak signing key accepted")
	}
	signed, err := SignVerificationAttempt(attempt, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyVerificationAttemptSignature(signed, []byte(strings.Repeat("x", 32))); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestVerificationAttemptSignatureAllowsAuditableFailureWithoutPromotion(t *testing.T) {
	attempt := validVerificationAttempt()
	attempt.Gates[0].Status = GateFailed
	key := []byte(strings.Repeat("z", 32))
	signed, err := SignVerificationAttempt(attempt, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyVerificationAttemptSignature(signed, key); err != nil {
		t.Fatal(err)
	}
	if err := signed.Passed([]string{"unit"}); err == nil {
		t.Fatal("signed failure must not permit promotion")
	}
}
