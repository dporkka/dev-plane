package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
)

func TestListOrganizations(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	userID := "user-1"
	rows := sqlmock.NewRows([]string{"id", "name", "slug", "plan", "settings", "created_at", "updated_at"}).
		AddRow("org-1", "Acme Corp", "acme", "pro", nil, time.Now(), time.Now()).
		AddRow("org-2", "Beta Inc", "beta", "free", nil, time.Now(), time.Now())

	mock.ExpectQuery("SELECT o.id, o.name, o.slug, o.plan, o.settings, o.created_at, o.updated_at").
		WithArgs(userID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/organizations", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: userID}))
	rec := httptest.NewRecorder()

	h.ListOrganizations(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var orgs []Organization
	if err := json.Unmarshal(rec.Body.Bytes(), &orgs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(orgs) != 2 {
		t.Fatalf("expected 2 orgs, got %d", len(orgs))
	}

	if orgs[0].Name != "Acme Corp" {
		t.Errorf("expected first org name 'Acme Corp', got %q", orgs[0].Name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestListOrganizations_Empty(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	userID := "user-1"
	rows := sqlmock.NewRows([]string{"id", "name", "slug", "plan", "settings", "created_at", "updated_at"})

	mock.ExpectQuery("SELECT o.id, o.name, o.slug, o.plan, o.settings, o.created_at, o.updated_at").
		WithArgs(userID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/organizations", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: userID}))
	rec := httptest.NewRecorder()

	h.ListOrganizations(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var orgs []Organization
	if err := json.Unmarshal(rec.Body.Bytes(), &orgs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if orgs == nil || len(orgs) != 0 {
		t.Fatalf("expected empty orgs, got %v", orgs)
	}
}

func TestListOrganizations_DBError(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	userID := "user-1"
	mock.ExpectQuery("SELECT o.id, o.name, o.slug, o.plan, o.settings, o.created_at, o.updated_at").
		WithArgs(userID).
		WillReturnError(errors.New("connection failed"))

	req := httptest.NewRequest(http.MethodGet, "/organizations", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: userID}))
	rec := httptest.NewRecorder()

	h.ListOrganizations(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCreateOrganization(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	userID := "user-1"
	mock.ExpectExec("INSERT INTO organizations").
		WithArgs(sqlmock.AnyArg(), "Acme Corp", "acme", "free", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	body, _ := json.Marshal(CreateOrganizationRequest{Name: "Acme Corp", Slug: "acme"})
	req := httptest.NewRequest(http.MethodPost, "/organizations", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: userID}))
	rec := httptest.NewRecorder()

	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}

	var org Organization
	if err := json.Unmarshal(rec.Body.Bytes(), &org); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if org.Name != "Acme Corp" {
		t.Errorf("expected org name 'Acme Corp', got %q", org.Name)
	}

	if org.Slug != "acme" {
		t.Errorf("expected org slug 'acme', got %q", org.Slug)
	}

	if org.Plan != "free" {
		t.Errorf("expected default plan 'free', got %q", org.Plan)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCreateOrganization_Invalid(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	// Missing name
	body, _ := json.Marshal(CreateOrganizationRequest{Slug: "acme"})
	req := httptest.NewRequest(http.MethodPost, "/organizations", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "user-1"}))
	rec := httptest.NewRecorder()

	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	// Missing slug
	body, _ = json.Marshal(CreateOrganizationRequest{Name: "Acme Corp"})
	req = httptest.NewRequest(http.MethodPost, "/organizations", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "user-1"}))
	rec = httptest.NewRecorder()

	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	// Empty body
	body, _ = json.Marshal(map[string]string{})
	req = httptest.NewRequest(http.MethodPost, "/organizations", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "user-1"}))
	rec = httptest.NewRecorder()

	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestCreateOrganization_DuplicateSlug(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	userID := "user-1"
	mock.ExpectExec("INSERT INTO organizations").
		WithArgs(sqlmock.AnyArg(), "Acme Corp", "acme", "free", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(errors.New("duplicate key value violates unique constraint \"organizations_slug_key\""))

	body, _ := json.Marshal(CreateOrganizationRequest{Name: "Acme Corp", Slug: "acme"})
	req := httptest.NewRequest(http.MethodPost, "/organizations", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: userID}))
	rec := httptest.NewRecorder()

	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetOrganization(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	orgID := "org-1"
	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "name", "slug", "plan", "settings", "created_at", "updated_at"}).
		AddRow(orgID, "Acme Corp", "acme", "pro", nil, now, now)

	mock.ExpectQuery("SELECT id, name, slug, plan, settings, created_at, updated_at").
		WithArgs(orgID).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/organizations/"+orgID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", orgID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.GetOrganization(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var org Organization
	if err := json.Unmarshal(rec.Body.Bytes(), &org); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if org.ID != orgID {
		t.Errorf("expected org ID %q, got %q", orgID, org.ID)
	}

	if org.Name != "Acme Corp" {
		t.Errorf("expected org name 'Acme Corp', got %q", org.Name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetOrganization_NotFound(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	orgID := "nonexistent"
	mock.ExpectQuery("SELECT id, name, slug, plan, settings, created_at, updated_at").
		WithArgs(orgID).
		WillReturnError(sql.ErrNoRows)

	req := httptest.NewRequest(http.MethodGet, "/organizations/"+orgID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", orgID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.GetOrganization(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
