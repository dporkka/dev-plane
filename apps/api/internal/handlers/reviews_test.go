package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func TestGetReview_NotFound(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	runID := "nonexistent-run"

	// authz.AuthorizeAgentRun queries the run with org check
	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs").
		WithArgs(runID).
		WillReturnError(sqlmock.ErrCancelled)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("runId", runID)
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/review", nil)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.GetReview(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

func TestGetReview_Success(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	runID := "run-1"
	now := time.Now()

	// Authz check: SELECT p.organization_id FROM agent_runs JOIN tasks JOIN projects
	mock.ExpectQuery("SELECT p.organization_id").
		WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).
			AddRow(testOrgID))

	// Reviewer.Get first query (bug: maps run_id to both columns, falls through)
	reviewCols := []string{"id", "run_id", "summary", "findings", "risk_level", "approvable",
		"suggestions", "test_coverage", "security_notes", "diff_summary", "created_at"}
	mock.ExpectQuery("SELECT id, run_id, summary, findings, risk_level, approvable").
		WithArgs(runID).
		WillReturnRows(sqlmock.NewRows(reviewCols).
			AddRow("rev-1", runID, "Good work", "[]", "low", true,
				"[]", "85%", "None", "[]", now))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("runId", runID)
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/review", nil)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.GetReview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
}

func TestGetReview_MissingRunID(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/runs//review", nil)
	req = req.WithContext(withTestUser(req.Context()))
	rec := httptest.NewRecorder()

	h.GetReview(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestRequestReview_Unauthorized(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	runID := "run-1"

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID+"/review", nil)
	rec := httptest.NewRecorder()

	h.RequestReview(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestRequestReview_RunNotFound(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	runID := "nonexistent-run"

	// Authz check fails
	mock.ExpectQuery("SELECT p.organization_id").
		WithArgs(runID).
		WillReturnError(sqlmock.ErrCancelled)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("runId", runID)
	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID+"/review", nil)
	req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	rec := httptest.NewRecorder()

	h.RequestReview(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}
