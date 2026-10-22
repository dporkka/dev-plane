//go:build linux

// Package deviceauth implements local device identity and short-lived proof-of-possession.
// It is transport-independent. Enrollment and per-tool authorization remain the
// responsibility of Dev Plane's authenticated control plane.
package deviceauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type Identity struct {
	ID      string
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

func IDFromPublicKey(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return "dp_" + hex.EncodeToString(sum[:])
}

func GenerateIdentity() (Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	return Identity{ID: IDFromPublicKey(pub), Public: pub, Private: priv}, nil
}

// CreateIdentityFile fails if the key already exists. A new key can invalidate
// device registrations; therefore overwriting is deliberately unsupported.
func CreateIdentityFile(path string) (Identity, error) {
	if path == "" {
		return Identity{}, errors.New("identity file path required")
	}
	identity, err := GenerateIdentity()
	if err != nil {
		return Identity{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(identity.Private)
	if err != nil {
		return Identity{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Identity{}, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return Identity{}, fmt.Errorf("create identity (refusing overwrite): %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err = f.Write(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		return Identity{}, err
	}
	if err = f.Sync(); err != nil {
		return Identity{}, err
	}
	if err = f.Close(); err != nil {
		return Identity{}, err
	}
	ok = true
	return identity, nil
}

func LoadIdentityFile(path string) (Identity, error) {
	if path == "" {
		return Identity{}, errors.New("identity file path required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Identity{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return Identity{}, errors.New("identity must be a regular owner-only file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return Identity{}, err
	}
	defer f.Close()
	if info, err = f.Stat(); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return Identity{}, errors.New("insecure identity file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return Identity{}, err
	}
	if len(data) > 4096 {
		return Identity{}, errors.New("identity file too large")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return Identity{}, errors.New("invalid identity encoding")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return Identity{}, errors.New("invalid private key")
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(priv) != ed25519.PrivateKeySize {
		return Identity{}, errors.New("identity must use Ed25519")
	}
	pub := priv.Public().(ed25519.PublicKey)
	return Identity{ID: IDFromPublicKey(pub), Public: pub, Private: priv}, nil
}
