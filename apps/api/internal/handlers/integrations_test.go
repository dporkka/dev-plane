package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/capability"
	secretstore "github.com/ai-dev-control-plane/api/internal/secrets"
	"github.com/ai-dev-control-plane/policies"
)

func TestCreateIntegrationWithoutToken(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectAuthorizeOrganization(mock, testOrgID)
	mock.ExpectExec("INSERT INTO integrations").
		WithArgs(sqlmock.AnyArg(), testOrgID, "slack", "Team Slack", []byte(`{"channel_id":"#alerts"}`), nil, sqlmock.AnyArg(), "pending", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	reqBody, _ := json.Marshal(CreateIntegrationRequest{
		IntegrationType: "slack",
		DisplayName:     "Team Slack",
		Config:          json.RawMessage(`{"channel_id":"#alerts"}`),
	})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgID", testOrgID)
	req := httptest.NewRequest(http.MethodPost, "/organizations/"+testOrgID+"/integrations", bytes.NewReader(reqBody))
	req = req.WithContext(withTestUser(req.Context()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.CreateIntegration(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestCreateIntegrationRequiresTypeAndName(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	reqBody, _ := json.Marshal(CreateIntegrationRequest{
		IntegrationType: "",
		DisplayName:     "",
	})
	req := httptest.NewRequest(http.MethodPost, "/organizations/"+testOrgID+"/integrations", bytes.NewReader(reqBody))
	req = req.WithContext(withTestUser(req.Context()))
	w := httptest.NewRecorder()

	h.CreateIntegration(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateIntegration(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	integrationID := "int-1"
	expectAuthorizeIntegration(mock, integrationID)
	mock.ExpectExec("UPDATE integrations SET").
		WithArgs("Updated Name", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), integrationID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id, organization_id, integration_type, display_name, config, status, webhook_url, last_synced_at, created_at, updated_at FROM integrations").
		WithArgs(integrationID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "integration_type", "display_name", "config", "status", "webhook_url", "last_synced_at", "created_at", "updated_at"}).
			AddRow(integrationID, testOrgID, "slack", "Updated Name", []byte(`{}`), "connected", nil, nil, time.Now().UTC(), time.Now().UTC()))

	displayName := "Updated Name"
	reqBody, _ := json.Marshal(UpdateIntegrationRequest{
		DisplayName: &displayName,
	})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", integrationID)
	req := httptest.NewRequest(http.MethodPatch, "/integrations/"+integrationID, bytes.NewReader(reqBody))
	req = req.WithContext(withTestUser(req.Context()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.UpdateIntegration(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp Integration
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.DisplayName != "Updated Name" {
		t.Fatalf("display_name = %q, want %q", resp.DisplayName, "Updated Name")
	}
	if resp.Status != "connected" {
		t.Fatalf("status = %q, want %q", resp.Status, "connected")
	}
}

func TestDeleteIntegration(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	integrationID := "int-1"
	expectAuthorizeIntegration(mock, integrationID)
	mock.ExpectExec("UPDATE integrations SET deleted_at").
		WithArgs(sqlmock.AnyArg(), integrationID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", integrationID)
	req := httptest.NewRequest(http.MethodDelete, "/integrations/"+integrationID, nil)
	req = req.WithContext(withTestUser(req.Context()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.DeleteIntegration(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func setupTestWithSecretManager(t *testing.T) (*Handler, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	allowAll := policies.NewEngine([]policies.Policy{
		{Name: "allow_all_tests", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	})
	keyring, err := secretstore.NewSingleKeyring("test-key", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("NewSingleKeyring() error: %v", err)
	}
	h := NewHandler(db, slog.Default()).
		WithCapabilityKernel(capability.NewKernel(allowAll, nil, nil, slog.Default())).
		WithSecretManager(secretstore.NewManager(db, keyring, nil, slog.Default()))
	cleanup := func() { db.Close() }
	return h, mock, cleanup
}

func TestVerifyIntegration(t *testing.T) {
	h, mock, cleanup := setupTestWithSecretManager(t)
	defer cleanup()

	integrationID := "int-1"
	aad := testOrgID + ":" + integrationID
	token := "test-token"
	ciphertext, err := h.secretManager.EncryptString(token, aad)
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}

	expectAuthorizeIntegration(mock, integrationID)
	mock.ExpectQuery("SELECT organization_id, integration_type, status, credentials_encrypted FROM integrations").
		WithArgs(integrationID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "integration_type", "status", "credentials_encrypted"}).
			AddRow(testOrgID, "slack", "pending", ciphertext))
	mock.ExpectExec("UPDATE integrations").
		WithArgs("connected", sqlmock.AnyArg(), integrationID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.WithIntegrationValidator(func(_ context.Context, integrationType string, tok, _ *string) error {
		if integrationType != "slack" || tok == nil || *tok != token {
			return errors.New("unexpected validation input")
		}
		return nil
	})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", integrationID)
	req := httptest.NewRequest(http.MethodPost, "/integrations/"+integrationID+"/verify", nil)
	req = req.WithContext(withTestUser(req.Context()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.VerifyIntegration(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["valid"] != true {
		t.Fatalf("valid = %v, want true", resp["valid"])
	}
	if resp["status"] != "connected" {
		t.Fatalf("status = %v, want connected", resp["status"])
	}
	if resp["error"] != "" {
		t.Fatalf("error = %v, want empty", resp["error"])
	}
}

func TestVerifyIntegrationInvalidToken(t *testing.T) {
	h, mock, cleanup := setupTestWithSecretManager(t)
	defer cleanup()

	integrationID := "int-1"
	aad := testOrgID + ":" + integrationID
	token := "test-token"
	ciphertext, err := h.secretManager.EncryptString(token, aad)
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}

	expectAuthorizeIntegration(mock, integrationID)
	mock.ExpectQuery("SELECT organization_id, integration_type, status, credentials_encrypted FROM integrations").
		WithArgs(integrationID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "integration_type", "status", "credentials_encrypted"}).
			AddRow(testOrgID, "slack", "pending", ciphertext))
	mock.ExpectExec("UPDATE integrations").
		WithArgs("error", sqlmock.AnyArg(), integrationID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.WithIntegrationValidator(func(context.Context, string, *string, *string) error {
		return errors.New("invalid token")
	})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", integrationID)
	req := httptest.NewRequest(http.MethodPost, "/integrations/"+integrationID+"/verify", nil)
	req = req.WithContext(withTestUser(req.Context()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.VerifyIntegration(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["valid"] != false {
		t.Fatalf("valid = %v, want false", resp["valid"])
	}
	if resp["status"] != "error" {
		t.Fatalf("status = %v, want error", resp["status"])
	}
	if resp["error"] != "invalid token" {
		t.Fatalf("error = %v, want invalid token", resp["error"])
	}
}

func TestVerifyIntegrationNoCredentials(t *testing.T) {
	h, mock, cleanup := setupTestWithSecretManager(t)
	defer cleanup()

	integrationID := "int-1"
	expectAuthorizeIntegration(mock, integrationID)
	mock.ExpectQuery("SELECT organization_id, integration_type, status, credentials_encrypted FROM integrations").
		WithArgs(integrationID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "integration_type", "status", "credentials_encrypted"}).
			AddRow(testOrgID, "slack", "pending", nil))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", integrationID)
	req := httptest.NewRequest(http.MethodPost, "/integrations/"+integrationID+"/verify", nil)
	req = req.WithContext(withTestUser(req.Context()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.VerifyIntegration(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}
