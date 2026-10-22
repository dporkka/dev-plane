package devices

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func testChallenge(t *testing.T) (Challenge, ed25519.PrivateKey) {
	t.Helper()
	pub, priv := testKey(t)
	c := Challenge{Nonce: "nonce_123", DeviceID: DeviceID(pub), OrganizationID: "org-1", UserID: "user-1", PublicKey: pub, IssuedUnix: 1000, ExpiresUnix: 1045}
	return c, priv
}

func TestDeviceIDUsesPublicKeyHash(t *testing.T) {
	pub, _ := testKey(t)
	sum := sha256.Sum256(pub)
	if got, want := DeviceID(pub), "dp_"+hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("id=%q want %q", got, want)
	}
}

func TestProofAcceptsOnlyExactChallengeAndSignature(t *testing.T) {
	c, priv := testChallenge(t)
	msg, err := EnrollmentMessage(c)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, msg)
	if err := VerifyEnrollmentProof(c, sig, time.Unix(1010, 0)); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func(*Challenge){
		func(c *Challenge) { c.UserID = "other" },
		func(c *Challenge) { c.OrganizationID = "other" },
		func(c *Challenge) { c.Nonce = "other" },
		func(c *Challenge) { c.DeviceID = "dp_wrong" },
		func(c *Challenge) { c.ExpiresUnix++ },
		func(c *Challenge) { c.PublicKey[0] ^= 0xff },
	} {
		mutated := c
		mutated.PublicKey = append([]byte(nil), c.PublicKey...)
		alter(&mutated)
		if err := VerifyEnrollmentProof(mutated, sig, time.Unix(1010, 0)); err == nil {
			t.Fatalf("tampered challenge accepted: %+v", mutated)
		}
	}
}

func TestProofRejectsExpiryAndBadKey(t *testing.T) {
	c, priv := testChallenge(t)
	msg, err := EnrollmentMessage(c)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, msg)
	for _, ts := range []int64{999, 1045, 1050} {
		if err := VerifyEnrollmentProof(c, sig, time.Unix(ts, 0)); !errors.Is(err, ErrInvalidProof) {
			t.Errorf("time=%d error=%v", ts, err)
		}
	}
	sig[0] ^= 1
	if err := VerifyEnrollmentProof(c, sig, time.Unix(1010, 0)); !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("bad sig: %v", err)
	}
}

func TestChallengeRejectsInvalidScopesAndLifetime(t *testing.T) {
	c, _ := testChallenge(t)
	for _, alter := range []func(*Challenge){
		func(c *Challenge) { c.OrganizationID = "" },
		func(c *Challenge) { c.UserID = "" },
		func(c *Challenge) { c.Nonce = "" },
		func(c *Challenge) { c.PublicKey = []byte{1} },
		func(c *Challenge) { c.ExpiresUnix = c.IssuedUnix },
		func(c *Challenge) { c.ExpiresUnix = c.IssuedUnix + 121 },
		func(c *Challenge) { c.DeviceID = "different" },
	} {
		x := c
		alter(&x)
		if _, err := EnrollmentMessage(x); err == nil {
			t.Fatalf("accepted invalid challenge %+v", x)
		}
	}
}
