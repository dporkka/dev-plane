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

func TestListProjects(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	orgID := "org-1"
	now := time.Now()

	expectAuthorizeOrganization(mock, orgID)
	mock.ExpectQuery("SELECT id, organization_id, name, slug, description, settings, created_at, updated_at").
		WithArgs(orgID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "name", "slug", "description", "settings", "created_at", "updated_at"}).
			AddRow("proj-1", orgID, "Project One", "project-one", nil, nil, now, now).
			AddRow("proj-2", orgID, "Project Two", "project-two", nil, nil, now, now))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgID", orgID)
	req := httptest.NewRequest(http.MethodGet, "/organizations/"+orgID+"/projects", nil)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.ListProjects(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var projects []Project
	if err := json.Unmarshal(rec.Body.Bytes(), &projects); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if projects[0].Name != "Project One" {
		t.Errorf("expected first project name 'Project One', got %q", projects[0].Name)
	}
}

func TestCreateProject(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	orgID := "org-1"

	expectAuthorizeOrganization(mock, orgID)
	// INSERT uses UUID and timestamps that we can't predict - use AnyArg
	mock.ExpectExec("INSERT INTO projects").
		WithArgs(sqlmock.AnyArg(), orgID, "New Project", "new-project", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	body, _ := json.Marshal(CreateProjectRequest{Name: "New Project", Slug: "new-project"})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgID", orgID)
	req := httptest.NewRequest(http.MethodPost, "/organizations/"+orgID+"/projects", bytes.NewReader(body))
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.CreateProject(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}
}

func TestGetProject_NotFound(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	projID := "nonexistent"

	expectAuthorizeProject(mock, projID)
	// Project lookup returns no rows
	mock.ExpectQuery("SELECT id, organization_id, name, slug, description, settings, created_at, updated_at").
		WithArgs(projID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "name", "slug", "description", "settings", "created_at", "updated_at"}))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", projID)
	req := httptest.NewRequest(http.MethodGet, "/projects/"+projID, nil)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.GetProject(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestListProjects_NoOrgID(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/organizations//projects", nil)
	req = req.WithContext(withTestUser(req.Context()))
	rec := httptest.NewRecorder()

	h.ListProjects(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestListProjects_Unauthorized(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	orgID := "org-1"
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgID", orgID)
	// No user in context
	req := httptest.NewRequest(http.MethodGet, "/organizations/"+orgID+"/projects", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ListProjects(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestGetProject_Success(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	projID := "proj-1"
	orgID := "org-1"
	now := time.Now()

	expectAuthorizeProject(mock, projID)
	mock.ExpectQuery("SELECT id, organization_id, name, slug, description, settings, created_at, updated_at").
		WithArgs(projID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "name", "slug", "description", "settings", "created_at", "updated_at"}).
			AddRow(projID, orgID, "My Project", "my-project", nil, nil, now, now))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", projID)
	req := httptest.NewRequest(http.MethodGet, "/projects/"+projID, nil)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.GetProject(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
}
