package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func TestCreateIntegrationWithoutToken(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectAuthorizeOrganization(mock, testOrgID)
	mock.ExpectExec("INSERT INTO integrations").
		WithArgs(sqlmock.AnyArg(), testOrgID, "slack", "Team Slack", []byte(`{"channel_id":"#alerts"}`), nil, nil, "pending", sqlmock.AnyArg()).
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
