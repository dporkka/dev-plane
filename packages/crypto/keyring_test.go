package crypto

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseKeyring(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB}, 32))
	kr, err := ParseKeyring("primary:" + key)
	if err != nil {
		t.Fatalf("parse keyring: %v", err)
	}
	if kr.PrimaryID() != "primary" {
		t.Fatalf("expected primary id, got %q", kr.PrimaryID())
	}
}

func TestParseKeyringRequires32Bytes(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB}, 16))
	_, err := ParseKeyring("primary:" + short)
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB}, 32))
	kr, err := ParseKeyring("primary:" + key)
	if err != nil {
		t.Fatalf("parse keyring: %v", err)
	}

	plaintext := "integration-token-123"
	aad := "org-1:integration-1"
	ciphertext, err := kr.Encrypt([]byte(plaintext), []byte(aad))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(ciphertext, "primary:") {
		t.Fatalf("ciphertext missing key id prefix: %q", ciphertext)
	}

	got, err := kr.Decrypt(ciphertext, []byte(aad))
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, string(got))
	}
}

func TestDecryptWrongAADFails(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB}, 32))
	kr, _ := ParseKeyring("primary:" + key)
	ciphertext, _ := kr.Encrypt([]byte("secret"), []byte("aad-1"))
	_, err := kr.Decrypt(ciphertext, []byte("aad-2"))
	if err == nil {
		t.Fatal("expected error decrypting with wrong aad")
	}
}

func TestDecryptUnknownKeyIDFails(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB}, 32))
	kr, _ := ParseKeyring("primary:" + key)
	ciphertext, _ := kr.Encrypt([]byte("secret"), []byte("aad"))
	ciphertext = "oldkey" + ciphertext[strings.Index(ciphertext, ":"):]
	_, err := kr.Decrypt(ciphertext, []byte("aad"))
	if err == nil {
		t.Fatal("expected error for unknown key id")
	}
}
