package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/audit"
	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/capability"
	secretstore "github.com/ai-dev-control-plane/api/internal/secrets"
	"github.com/ai-dev-control-plane/policies"
)

func TestSecretHandlersCreateListAndRotateEncryptedSecrets(t *testing.T) {
	db := setupSecretHandlerDB(t)
	defer db.Close()
	h := setupSecretHandler(t, db)

	createBody, _ := json.Marshal(CreateSecretRequest{
		Name:        "github-token",
		Scope:       "staging",
		Description: "GitHub token",
		Value:       "ghp_plaintext",
	})
	createReq := secretRequest(http.MethodPost, "/organizations/org-1/secrets", "org-1", "", bytes.NewReader(createBody))
	createRec := httptest.NewRecorder()
	h.CreateSecret(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateSecret status = %d body=%s", createRec.Code, createRec.Body.String())
	}
	if strings.Contains(createRec.Body.String(), "ghp_plaintext") || strings.Contains(createRec.Body.String(), "ciphertext") {
		t.Fatalf("create response leaked secret material: %s", createRec.Body.String())
	}
	var created SecretResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Provider != "encrypted_db" {
		t.Fatalf("provider = %q, want encrypted_db", created.Provider)
	}

	var ciphertext string
	if err := db.QueryRow(`SELECT ciphertext FROM secret_values WHERE secret_reference_id = ? AND active = true`, created.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("query ciphertext: %v", err)
	}
	if strings.Contains(ciphertext, "ghp_plaintext") {
		t.Fatalf("ciphertext contains plaintext: %q", ciphertext)
	}

	listReq := secretRequest(http.MethodGet, "/organizations/org-1/secrets", "org-1", "", nil)
	listRec := httptest.NewRecorder()
	h.ListSecrets(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListSecrets status = %d body=%s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "ghp_plaintext") || strings.Contains(listRec.Body.String(), "ciphertext") {
		t.Fatalf("list response leaked secret material: %s", listRec.Body.String())
	}

	rotateBody, _ := json.Marshal(RotateSecretRequest{Value: "rotated_plaintext"})
	rotateReq := secretRequest(http.MethodPost, "/secrets/"+created.ID+"/rotate", "", created.ID, bytes.NewReader(rotateBody))
	rotateRec := httptest.NewRecorder()
	h.RotateSecret(rotateRec, rotateReq)
	if rotateRec.Code != http.StatusOK {
		t.Fatalf("RotateSecret status = %d body=%s", rotateRec.Code, rotateRec.Body.String())
	}
	var activeCount, inactiveCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM secret_values WHERE secret_reference_id = ? AND active = true`, created.ID).Scan(&activeCount); err != nil {
		t.Fatalf("query active count: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM secret_values WHERE secret_reference_id = ? AND active = false`, created.ID).Scan(&inactiveCount); err != nil {
		t.Fatalf("query inactive count: %v", err)
	}
	if activeCount != 1 || inactiveCount != 1 {
		t.Fatalf("active/inactive = %d/%d, want 1/1", activeCount, inactiveCount)
	}

	for _, action := range []string{"capability_check", "secret.write", "secret.rotate"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ?`, action).Scan(&count); err != nil {
			t.Fatalf("query audit action %s: %v", action, err)
		}
		if count == 0 {
			t.Fatalf("audit action %s missing", action)
		}
	}
}

func TestCreateSecretWithoutManagerReturnsUnavailable(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	body, _ := json.Marshal(CreateSecretRequest{Name: "x", Value: "secret"})
	req := secretRequest(http.MethodPost, "/organizations/org-1/secrets", "org-1", "", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.CreateSecret(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func setupSecretHandler(t *testing.T, db *sql.DB) *Handler {
	t.Helper()
	allowAll := policies.NewEngine([]policies.Policy{
		{Name: "allow_all_tests", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditLogger := audit.NewLogger(db, logger)
	keyring, err := secretstore.NewSingleKeyring("test-key", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("NewSingleKeyring() error: %v", err)
	}
	return NewHandler(db, logger).
		WithCapabilityKernel(capability.NewKernel(allowAll, nil, auditLogger, logger)).
		WithSecretManager(secretstore.NewManager(db, keyring, auditLogger, logger))
}

func setupSecretHandlerDB(t *testing.T) *sql.DB {
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

func secretRequest(method, target, orgID, secretID string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	rctx := chi.NewRouteContext()
	if orgID != "" {
		rctx.URLParams.Add("orgID", orgID)
	}
	if secretID != "" {
		rctx.URLParams.Add("id", secretID)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req.WithContext(auth.WithUser(req.Context(), &auth.Claims{
		UserID: "user-1",
		OrgID:  "org-1",
		Email:  "user@example.invalid",
		Role:   "owner",
	}))
}
