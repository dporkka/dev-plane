package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func TestListPoliciesRequiresAdmin(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		wantStatus int
	}{
		{"owner can list", "owner", http.StatusOK},
		{"admin can list", "admin", http.StatusOK},
		{"member cannot list", "member", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mock, cleanup := setupTest(t)
			defer cleanup()

			expectAuthorizeOrganization(mock, "org-1")
			if tt.wantStatus == http.StatusOK {
				rows := sqlmock.NewRows([]string{"id", "organization_id", "project_id", "name", "resource_type", "action", "effect", "conditions", "priority", "created_at", "updated_at"})
				mock.ExpectQuery("SELECT id, organization_id, project_id, name, resource_type, action, effect").
					WithArgs("org-1").
					WillReturnRows(rows)
			}

			req := httptest.NewRequest(http.MethodGet, "/organizations/org-1/policies", nil)
			req = req.WithContext(withRole(req.Context(), tt.role))
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("orgID", "org-1")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()

			h.ListPolicies(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}

func TestCreatePolicyRequiresAdmin(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		wantStatus int
	}{
		{"owner can create", "owner", http.StatusCreated},
		{"admin can create", "admin", http.StatusCreated},
		{"member cannot create", "member", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mock, cleanup := setupTest(t)
			defer cleanup()

			expectAuthorizeOrganization(mock, "org-1")
			if tt.wantStatus == http.StatusCreated {
				mock.ExpectExec("INSERT INTO policies").
					WithArgs(sqlmock.AnyArg(), "org-1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(1, 1))
			}

			body, _ := json.Marshal(CreatePolicyRequest{
				Name:         "test-policy",
				ResourceType: "file",
				Action:       "read",
				Effect:       "allow",
			})
			req := httptest.NewRequest(http.MethodPost, "/organizations/org-1/policies", bytes.NewReader(body))
			req = req.WithContext(withRole(req.Context(), tt.role))
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("orgID", "org-1")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()

			h.CreatePolicy(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled expectations: %v", err)
			}
		})
	}
}

func TestListPoliciesReturnsPolicies(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectAuthorizeOrganization(mock, "org-1")
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "organization_id", "project_id", "name", "resource_type", "action", "effect", "conditions", "priority", "created_at", "updated_at"}).
		AddRow("policy-1", "org-1", nil, "allow-reads", "file", "read", "allow", nil, 100, now, now)
	mock.ExpectQuery("SELECT id, organization_id, project_id, name, resource_type, action, effect").
		WithArgs("org-1").
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/organizations/org-1/policies", nil)
	req = req.WithContext(withRole(req.Context(), "admin"))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgID", "org-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ListPolicies(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var policies []Policy
	if err := json.Unmarshal(rec.Body.Bytes(), &policies); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
	if policies[0].Name != "allow-reads" {
		t.Errorf("name = %q, want allow-reads", policies[0].Name)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
