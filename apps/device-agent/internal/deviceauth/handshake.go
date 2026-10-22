//go:build linux

package deviceauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

const audience = "dev-plane/device-session/v1"
const challengeLifetime = 45 * time.Second

// Challenge contains only server-generated values. It never supplies a tool grant.
type Challenge struct {
	DeviceID  string `json:"deviceId"`
	Nonce     string `json:"nonce"`
	Audience  string `json:"audience"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

type Proof struct {
	Challenge Challenge `json:"challenge"`
	Signature []byte    `json:"signature"`
}

func challengeMessage(c Challenge) ([]byte, error) {
	if c.Audience != audience || c.DeviceID == "" || c.Nonce == "" || c.ExpiresAt <= c.IssuedAt {
		return nil, errors.New("invalid challenge")
	}
	return []byte(fmt.Sprintf("%s\n%s\n%s\n%d\n%d", c.Audience, c.DeviceID, c.Nonce, c.IssuedAt, c.ExpiresAt)), nil
}

func SignChallenge(identity Identity, c Challenge) (Proof, error) {
	if c.DeviceID != identity.ID {
		return Proof{}, errors.New("challenge is for another device")
	}
	message, err := challengeMessage(c)
	if err != nil {
		return Proof{}, err
	}
	if len(identity.Private) != ed25519.PrivateKeySize {
		return Proof{}, errors.New("invalid local key")
	}
	return Proof{Challenge: c, Signature: ed25519.Sign(identity.Private, message)}, nil
}

// Verifier is an in-memory protocol verifier, not a persistent enrollment
// registry. Its Register method must only be called by an authenticated API.
// A production gateway must store enrollments and revocations durably.
type Verifier struct {
	mu         sync.Mutex
	now        func() time.Time
	registered map[string]ed25519.PublicKey
	pending    map[string]Challenge
}

func NewVerifier(now func() time.Time) *Verifier {
	if now == nil {
		now = time.Now
	}
	return &Verifier{now: now, registered: make(map[string]ed25519.PublicKey), pending: make(map[string]Challenge)}
}

func (v *Verifier) Register(id string, public ed25519.PublicKey) error {
	if len(public) != ed25519.PublicKeySize || id != IDFromPublicKey(public) {
		return errors.New("device ID does not match public key")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, exists := v.registered[id]; exists {
		return errors.New("device already enrolled")
	}
	v.registered[id] = append(ed25519.PublicKey(nil), public...)
	return nil
}
func (v *Verifier) Revoke(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.registered[id]; !ok {
		return errors.New("unknown device")
	}
	delete(v.registered, id)
	for key, c := range v.pending {
		if c.DeviceID == id {
			delete(v.pending, key)
		}
	}
	return nil
}
func (v *Verifier) Challenge(id string) (Challenge, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.registered[id]; !ok {
		return Challenge{}, errors.New("unknown device")
	}
	current := v.now()
	for nonce, pending := range v.pending {
		if current.Unix() >= pending.ExpiresAt || pending.DeviceID == id {
			delete(v.pending, nonce)
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return Challenge{}, err
	}
	c := Challenge{DeviceID: id, Nonce: base64.RawURLEncoding.EncodeToString(buf), Audience: audience, IssuedAt: current.Unix(), ExpiresAt: current.Add(challengeLifetime).Unix()}
	v.pending[c.Nonce] = c
	return c, nil
}
func (v *Verifier) Verify(proof Proof) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	expected, exists := v.pending[proof.Challenge.Nonce]
	if !exists || expected != proof.Challenge {
		return errors.New("unknown or altered challenge")
	}
	key, ok := v.registered[expected.DeviceID]
	if !ok {
		return errors.New("device revoked or unknown")
	}
	current := v.now().Unix()
	if current < expected.IssuedAt || current >= expected.ExpiresAt {
		return errors.New("expired or not-yet-valid challenge")
	}
	message, err := challengeMessage(proof.Challenge)
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, message, proof.Signature) {
		return errors.New("invalid device signature")
	}
	delete(v.pending, expected.Nonce)
	return nil
}
