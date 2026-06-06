package secrets

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/internal/audit"
)

func TestParseKeyring(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	encoded := base64.StdEncoding.EncodeToString(key)
	keyring, err := ParseKeyring("primary:" + encoded)
	if err != nil {
		t.Fatalf("ParseKeyring() error: %v", err)
	}
	if keyring.primary.ID != "primary" {
		t.Fatalf("primary id = %q, want primary", keyring.primary.ID)
	}

	if _, err := ParseKeyring(""); err == nil {
		t.Fatal("ParseKeyring(empty) error = nil, want error")
	}
	if _, err := ParseKeyring("bad:not-base64"); err == nil {
		t.Fatal("ParseKeyring(bad) error = nil, want error")
	}
	if _, err := ParseKeyring("short:" + base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("ParseKeyring(short) error = nil, want error")
	}
}

func TestManagerStoreResolveRotateAndAudit(t *testing.T) {
	db := setupSecretTestDB(t)
	defer db.Close()
	keyring, err := NewSingleKeyring("k1", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatalf("NewSingleKeyring() error: %v", err)
	}
	manager := NewManager(db, keyring, audit.NewLogger(db, slog.Default()), slog.Default())
	ctx := context.Background()

	stored, err := manager.Store(ctx, StoreRequest{
		OrganizationID: "org-1",
		Name:           "github-token",
		Scope:          "prod",
		Value:          []byte("ghp_plaintext_secret"),
		ActorType:      "human",
		ActorID:        "user-1",
	})
	if err != nil {
		t.Fatalf("Store() error: %v", err)
	}
	if stored.Reference.Provider != "encrypted_db" {
		t.Fatalf("Provider = %q, want encrypted_db", stored.Reference.Provider)
	}
	if stored.Version != 1 {
		t.Fatalf("Version = %d, want 1", stored.Version)
	}

	var ciphertext string
	if err := db.QueryRow(`SELECT ciphertext FROM secret_values WHERE secret_reference_id = ? AND active = true`, stored.Reference.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("query ciphertext: %v", err)
	}
	if strings.Contains(ciphertext, "ghp_plaintext_secret") {
		t.Fatalf("ciphertext contains plaintext: %q", ciphertext)
	}

	plaintext, err := manager.Resolve(ctx, stored.Reference.ID, "agent", "run-1")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if string(plaintext) != "ghp_plaintext_secret" {
		t.Fatalf("Resolve() = %q", string(plaintext))
	}

	rotated, err := manager.Rotate(ctx, RotationRequest{
		SecretID:  stored.Reference.ID,
		Value:     []byte("rotated_secret"),
		ActorType: "human",
		ActorID:   "user-2",
	})
	if err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}
	if rotated.Version != 2 {
		t.Fatalf("rotated version = %d, want 2", rotated.Version)
	}
	plaintext, err = manager.Resolve(ctx, stored.Reference.ID, "agent", "run-2")
	if err != nil {
		t.Fatalf("Resolve(rotated) error: %v", err)
	}
	if string(plaintext) != "rotated_secret" {
		t.Fatalf("Resolve(rotated) = %q", string(plaintext))
	}

	var activeCount, inactiveCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM secret_values WHERE secret_reference_id = ? AND active = true`, stored.Reference.ID).Scan(&activeCount); err != nil {
		t.Fatalf("query active count: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM secret_values WHERE secret_reference_id = ? AND active = false`, stored.Reference.ID).Scan(&inactiveCount); err != nil {
		t.Fatalf("query inactive count: %v", err)
	}
	if activeCount != 1 || inactiveCount != 1 {
		t.Fatalf("active/inactive count = %d/%d, want 1/1", activeCount, inactiveCount)
	}

	for _, action := range []string{"secret.write", "secret.read", "secret.rotate"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ?`, action).Scan(&count); err != nil {
			t.Fatalf("query audit %s: %v", action, err)
		}
		if count == 0 {
			t.Fatalf("audit action %s missing", action)
		}
	}
}

func TestManagerRequiresConfiguredKeyring(t *testing.T) {
	db := setupSecretTestDB(t)
	defer db.Close()
	manager := NewManager(db, nil, nil, slog.Default())
	if _, err := manager.Store(context.Background(), StoreRequest{OrganizationID: "org-1", Name: "x", Value: []byte("secret")}); err == nil {
		t.Fatal("Store() error = nil, want keyring error")
	}
}

func setupSecretTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE secret_references (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			project_id TEXT,
			name TEXT NOT NULL,
			scope TEXT NOT NULL,
			provider TEXT NOT NULL,
			key_path TEXT NOT NULL,
			description TEXT,
			last_rotated_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE secret_values (
			id TEXT PRIMARY KEY,
			secret_reference_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			key_id TEXT NOT NULL,
			ciphertext TEXT NOT NULL,
			active BOOLEAN NOT NULL DEFAULT true,
			created_at DATETIME NOT NULL,
			rotated_at DATETIME,
			UNIQUE(secret_reference_id, version)
		);
		CREATE TABLE audit_logs (
			id TEXT PRIMARY KEY,
			organization_id TEXT,
			actor_type TEXT NOT NULL,
			actor_id TEXT,
			action TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT,
			details TEXT,
			created_at DATETIME
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}
