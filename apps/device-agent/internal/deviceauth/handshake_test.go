package deviceauth

import (
	"crypto/ed25519"
	"testing"
	"time"
)

func TestChallengeProofSucceedsOnce(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	identity, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewVerifier(func() time.Time { return now })
	if err := verifier.Register(identity.ID, identity.Public); err != nil {
		t.Fatal(err)
	}
	challenge, err := verifier.Challenge(identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := SignChallenge(identity, challenge)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(proof); err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(proof); err == nil {
		t.Fatal("accepted replay")
	}
}

func TestRejectUnknownRevokedForgedExpiredOrMismatched(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	verifier := NewVerifier(func() time.Time { return now })
	id, _ := GenerateIdentity()
	other, _ := GenerateIdentity()
	if _, err := verifier.Challenge(id.ID); err == nil {
		t.Fatal("challenged unknown device")
	}
	if err := verifier.Register("arbitrary", id.Public); err == nil {
		t.Fatal("accepted spoofed identifier")
	}
	if err := verifier.Register(id.ID, id.Public); err != nil {
		t.Fatal(err)
	}
	challenge, err := verifier.Challenge(id.ID)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := SignChallenge(id, challenge)
	if err != nil {
		t.Fatal(err)
	}
	proof.Signature = ed25519.Sign(other.Private, mustChallengeMessage(t, challenge))
	if err := verifier.Verify(proof); err == nil {
		t.Fatal("accepted forged signature")
	}
	proof, _ = SignChallenge(id, challenge)
	now = now.Add(2 * time.Minute)
	if err := verifier.Verify(proof); err == nil {
		t.Fatal("accepted expired proof")
	}
	now = now.Add(-2 * time.Minute)
	challenge2, err := verifier.Challenge(id.ID)
	if err != nil {
		t.Fatal(err)
	}
	proof, _ = SignChallenge(id, challenge2)
	if err := verifier.Revoke(id.ID); err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(proof); err == nil {
		t.Fatal("accepted revoked device")
	}
}

func TestTamperedChallengeRejected(t *testing.T) {
	verifier := NewVerifier(time.Now)
	id, _ := GenerateIdentity()
	_ = verifier.Register(id.ID, id.Public)
	challenge, _ := verifier.Challenge(id.ID)
	proof, _ := SignChallenge(id, challenge)
	proof.Challenge.Audience = "untrusted"
	if err := verifier.Verify(proof); err == nil {
		t.Fatal("accepted altered audience")
	}
	proof, _ = SignChallenge(id, challenge)
	proof.Challenge.Nonce = "another"
	if err := verifier.Verify(proof); err == nil {
		t.Fatal("accepted unknown nonce")
	}
}

func mustChallengeMessage(t *testing.T, c Challenge) []byte {
	t.Helper()
	b, err := challengeMessage(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFreshChallengeInvalidatesPreviousProofAndExpiresAtBoundary(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	verifier := NewVerifier(func() time.Time { return now })
	id, _ := GenerateIdentity()
	if err := verifier.Register(id.ID, id.Public); err != nil {
		t.Fatal(err)
	}
	first, _ := verifier.Challenge(id.ID)
	firstProof, _ := SignChallenge(id, first)
	second, _ := verifier.Challenge(id.ID)
	if err := verifier.Verify(firstProof); err == nil {
		t.Fatal("older challenge must be invalidated")
	}
	secondProof, _ := SignChallenge(id, second)
	now = time.Unix(second.ExpiresAt, 0)
	if err := verifier.Verify(secondProof); err == nil {
		t.Fatal("expiration boundary accepted")
	}
}
