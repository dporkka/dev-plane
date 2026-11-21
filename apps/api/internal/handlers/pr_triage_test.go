package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func triageRequest(method, body string, role string) *http.Request {
	req := httptest.NewRequest(method, "/organizations/"+testOrgID+"/pr-triage", strings.NewReader(body))
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("orgID", testOrgID)
	return req.WithContext(withRole(context.WithValue(req.Context(), chi.RouteCtxKey, ctx), role))
}

func TestImportPRTriageRequiresAdmin(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectAuthorizeOrganization(mock, testOrgID)
	rec := httptest.NewRecorder()
	h.ImportPRTriage(rec, triageRequest(http.MethodPost, "{}", "member"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportPRTriageRejectsStaleAdminClaims(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectAuthorizeOrganization(mock, testOrgID)
	mock.ExpectQuery("SELECT role FROM users").WithArgs(testUserID, testOrgID).
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("member"))
	rec := httptest.NewRecorder()
	h.ImportPRTriage(rec, triageRequest(http.MethodPost, "{}", "owner"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportPRTriageRejectsUnknownRepositoryWithoutWrites(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectAuthorizeOrganization(mock, testOrgID)
	mock.ExpectQuery("SELECT role FROM users").WithArgs(testUserID, testOrgID).
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("owner"))
	mock.ExpectQuery("(?s)SELECT r.id, r.full_name FROM repositories").
		WithArgs(testOrgID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "full_name"}).AddRow("repo-id", "owner/known"))
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"generated_at":%q,"decisions":[{"pr":{"repo":"owner/unknown","number":1,"title":"Changes","url":"https://github.com/owner/unknown/pull/1","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefName":"main"},"bucket":"review","score":10,"reasons":["needs review"]}]}`, time.Now().UTC().Format(time.RFC3339))
	h.ImportPRTriage(rec, triageRequest(http.MethodPost, body, "owner"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListPRTriageRejectsOtherOrganizations(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	mock.ExpectQuery("SELECT EXISTS").WithArgs(testUserID, testOrgID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	rec := httptest.NewRecorder()
	h.ListPRTriage(rec, triageRequest(http.MethodGet, "", "member"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportPRTriageStoresAuthorizedAdvisorySnapshot(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectAuthorizeOrganization(mock, testOrgID)
	mock.ExpectQuery("SELECT role FROM users").WithArgs(testUserID, testOrgID).
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("owner"))
	mock.ExpectQuery("(?s)SELECT r.id, r.full_name FROM repositories").
		WithArgs(testOrgID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "full_name"}).AddRow("repo-id", "owner/repo"))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO external_pr_triage").
		WithArgs("repo-id", testOrgID, 1, "Review change",
			"https://github.com/owner/repo/pull/1", strings.Repeat("a", 40), "main",
			"review", 20, false, `["needs review"]`, "[]", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	now := time.Now().UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"generated_at":%q,"decisions":[{"pr":{"repo":"owner/repo","number":1,"title":"Review change","url":"https://github.com/owner/repo/pull/1","headRefOid":%q,"baseRefName":"main"},"bucket":"review","score":20,"reasons":["needs review"]}]}`, now, strings.Repeat("a", 40))
	rec := httptest.NewRecorder()
	h.ImportPRTriage(rec, triageRequest(http.MethodPost, body, "owner"))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"advisory_only":true`) {
		t.Fatal(rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListPRTriageScopesRecordsToOrganization(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectAuthorizeOrganization(mock, testOrgID)
	now := time.Now().UTC()
	mock.ExpectQuery("(?s)SELECT r.full_name, e.number").
		WithArgs(testOrgID, "review", "", 51, 0).
		WillReturnRows(sqlmock.NewRows([]string{"full_name", "number", "title", "url", "head_sha", "base_branch", "bucket", "score", "draft", "reasons", "dependencies", "snapshot_at"}).
			AddRow("owner/repo", 1, "Review change", "https://github.com/owner/repo/pull/1", strings.Repeat("a", 40),
				"main", "review", 20, false, `["needs review"]`, "[]", now))
	req := triageRequest(http.MethodGet, "", "member")
	req.URL.RawQuery = "bucket=review"
	rec := httptest.NewRecorder()
	h.ListPRTriage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"repo":"owner/repo"`) {
		t.Fatal(rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"completeness_unverified":true`) {
		t.Fatal(rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPRTriageSnapshotValidationRejectsSpoofedURLsAndDuplicates(t *testing.T) {
	s := importedPRTriageSnapshot{GeneratedAt: time.Now().UTC()}
	body := fmt.Sprintf(`{"generated_at":%q,"decisions":[{"pr":{"repo":"owner/repo","number":1,"title":"Check","url":"https://evil.example/owner/repo/pull/1","headRefOid":%q,"baseRefName":"main"},"bucket":"candidate","score":12}]}`, time.Now().UTC().Format(time.RFC3339), strings.Repeat("a", 40))
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	if err := validPRTriageSnapshot(s, time.Now().UTC()); err == nil {
		t.Fatal("spoofed URL accepted")
	}
	s.Decisions[0].PR.URL = "https://github.com/owner/repo/pull/1"
	s.Decisions = append(s.Decisions, s.Decisions[0])
	if err := validPRTriageSnapshot(s, time.Now().UTC()); err == nil {
		t.Fatal("duplicate PR accepted")
	}
}
