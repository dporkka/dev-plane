// Package crypto provides shared cryptographic primitives for the AI Dev Control Plane.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	// ErrKey is returned when the keyring is misconfigured or a key lookup fails.
	ErrKey = errors.New("encryption key error")
)

// Key represents a single AEAD key.
type Key struct {
	ID    string
	Value []byte
}

// Keyring holds one or more AES-256-GCM keys. The first key supplied to
// ParseKeyring is the primary and is used for all new encryptions; older keys
// are retained so previously encrypted values can still be decrypted.
type Keyring struct {
	primary Key
	keys    map[string][]byte
}

// ParseKeyring parses comma-separated key specs in the form
// key-id:base64-encoded-32-byte-key. The first key is used for new writes.
func ParseKeyring(raw string) (*Keyring, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: keyring is required", ErrKey)
	}
	keyring := &Keyring{keys: map[string][]byte{}}
	for i, spec := range strings.Split(raw, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		id, encoded, ok := strings.Cut(spec, ":")
		if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(encoded) == "" {
			return nil, fmt.Errorf("%w: key spec must be key-id:base64-key", ErrKey)
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return nil, fmt.Errorf("%w: decode key %q: %w", ErrKey, id, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%w: key %q must decode to 32 bytes", ErrKey, id)
		}
		id = strings.TrimSpace(id)
		keyring.keys[id] = key
		if i == 0 {
			keyring.primary = Key{ID: id, Value: key}
		}
	}
	if keyring.primary.ID == "" {
		return nil, fmt.Errorf("%w: no keys configured", ErrKey)
	}
	return keyring, nil
}

// NewSingleKeyring creates a keyring with a single key.
func NewSingleKeyring(id string, key []byte) (*Keyring, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: key id is required", ErrKey)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: key must be 32 bytes", ErrKey)
	}
	return &Keyring{
		primary: Key{ID: id, Value: append([]byte(nil), key...)},
		keys:    map[string][]byte{id: append([]byte(nil), key...)},
	}, nil
}

// Encrypt encrypts plaintext using the primary key and returns a string of the
// form "keyID:base64(nonce||ciphertext)". The aad is authenticated but not
// encrypted; it must be supplied again during decryption.
func (k *Keyring) Encrypt(plaintext, aad []byte) (string, error) {
	block, err := aes.NewCipher(k.primary.Value)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	payload := append(nonce, sealed...)
	return k.primary.ID + ":" + base64.StdEncoding.EncodeToString(payload), nil
}

// Decrypt decrypts a ciphertext produced by Encrypt. The aad must match the
// value used during encryption.
func (k *Keyring) Decrypt(ciphertext string, aad []byte) ([]byte, error) {
	keyID, encoded, ok := strings.Cut(ciphertext, ":")
	if !ok {
		return nil, fmt.Errorf("%w: invalid ciphertext format", ErrKey)
	}
	key, ok := k.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: key %q not configured", ErrKey, keyID)
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: decode ciphertext: %w", ErrKey, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(payload) < gcm.NonceSize() {
		return nil, fmt.Errorf("%w: ciphertext payload too short", ErrKey)
	}
	nonce := payload[:gcm.NonceSize()]
	sealed := payload[gcm.NonceSize():]
	return gcm.Open(nil, nonce, sealed, aad)
}

// PrimaryID returns the ID of the key used for new encryptions.
func (k *Keyring) PrimaryID() string {
	return k.primary.ID
}
