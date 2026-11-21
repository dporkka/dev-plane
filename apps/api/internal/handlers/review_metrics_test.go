package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func newReviewMetricsRequest(url string, authenticated bool) *http.Request {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgID", testOrgID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if authenticated {
		ctx = withTestUser(ctx)
	}
	return req.WithContext(ctx)
}

func TestGetReviewMetrics_SuccessScopedCounts(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectAuthorizeOrganization(mock, testOrgID)
	mock.ExpectQuery("WITH selected AS").
		WithArgs(testOrgID, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), nil).
		WillReturnRows(sqlmock.NewRows([]string{"reviewed_prs", "approvable_prs", "critical_findings", "high_findings", "medium_findings", "low_findings", "info_findings", "security_findings"}).
			AddRow(4, 3, 1, 2, 3, 4, 5, 2))
	req := newReviewMetricsRequest("/organizations/org-1/review-metrics?start=2026-11-01&end=2026-11-30", true)
	rec := httptest.NewRecorder()
	h.GetReviewMetrics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result ReviewMetrics
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ReviewedPRs != 4 || result.SecurityFindings != 2 || result.CriticalFindings != 1 {
		t.Fatalf("wrong result: %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetReviewMetrics_AuthAndRangeValidation(t *testing.T) {
	for _, tc := range []struct {
		name, url     string
		authenticated bool
		want          int
	}{
		{"unauthenticated", "/organizations/org-1/review-metrics?start=2026-11-01&end=2026-11-30", false, http.StatusUnauthorized},
		{"missing range", "/organizations/org-1/review-metrics", true, http.StatusBadRequest},
		{"reversed range", "/organizations/org-1/review-metrics?start=2026-11-30&end=2026-11-01", true, http.StatusBadRequest},
		{"invalid repository", "/organizations/org-1/review-metrics?start=2026-11-01&end=2026-11-30&repository=not-uuid", true, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, cleanup := setupTest(t)
			defer cleanup()
			rec := httptest.NewRecorder()
			h.GetReviewMetrics(rec, newReviewMetricsRequest(tc.url, tc.authenticated))
			if rec.Code != tc.want {
				t.Fatalf("status=%d, want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestGetReviewMetrics_CrossOrgIsInvisible(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	mock.ExpectQuery("SELECT EXISTS").WithArgs(testUserID, testOrgID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	rec := httptest.NewRecorder()
	h.GetReviewMetrics(rec, newReviewMetricsRequest("/organizations/org-1/review-metrics?start=2026-11-01&end=2026-11-30", true))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
