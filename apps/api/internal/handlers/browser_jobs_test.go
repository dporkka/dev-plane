package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/auth"
	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/go-chi/chi/v5"
)

func browserHandlerFixture(t *testing.T) (*BrowserJobHandler, func(string, string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	d, err := dbpkg.New("file:" + filepath.Join(t.TempDir(), "http-jobs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err = d.RunMigrations("../../../../packages/db/migrations"); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"org-a", "org-b"} {
		if _, err = d.Exec(`INSERT INTO organizations(id,name,slug) VALUES(?,?,?)`, o, o, o); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = d.Exec(`INSERT INTO users(id,organization_id,email,role) VALUES('user-a','org-a','a@demo.test','owner')`); err != nil {
		t.Fatal(err)
	}
	h := NewBrowserJobHandler(d.DB, []string{"https://example.com"})
	exec := func(org, role, method, body string) *httptest.ResponseRecorder {
		path := "/organizations/" + org + "/browser-jobs"
		if method == http.MethodGet {
			path += "/jobid"
		}
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		rc := chi.NewRouteContext()
		rc.URLParams.Add("orgID", org)
		if method == http.MethodGet {
			rc.URLParams.Add("id", "jobid")
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
		if role != "unauthenticated" {
			req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "user-a", OrgID: "org-a", Role: role}))
		}
		rec := httptest.NewRecorder()
		if method == http.MethodGet {
			h.Get(rec, req)
		} else {
			h.Create(rec, req)
		}
		return rec
	}
	return h, exec
}

const validBrowserBody = `{"idempotency_key":"idem-1","start_url":"https://example.com/status","checks":[{"type":"text_visible","value":"Ready"}],"timeout_ms":15000,"capture":{"screenshot":true,"trace":true}}`

func TestBrowserJobAPIRequiresValidTenantAndAdmin(t *testing.T) {
	_, request := browserHandlerFixture(t)
	for _, tc := range []struct {
		org, role string
		status    int
	}{
		{"org-a", "unauthenticated", http.StatusUnauthorized},
		{"org-a", "member", http.StatusForbidden},
		{"org-b", "owner", http.StatusNotFound},
	} {
		rec := request(tc.org, tc.role, http.MethodPost, validBrowserBody)
		if rec.Code != tc.status {
			t.Errorf("%s/%s status=%d body=%s", tc.org, tc.role, rec.Code, rec.Body.String())
		}
	}
}

func TestBrowserJobAPIPersistsIdempotentValidatedSubmission(t *testing.T) {
	_, request := browserHandlerFixture(t)
	first := request("org-a", "owner", http.MethodPost, validBrowserBody)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	var got struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &got); err != nil || got.ID == "" || got.State != "queued" {
		t.Fatalf("result %+v %v", got, err)
	}
	again := request("org-a", "owner", http.MethodPost, validBrowserBody)
	if again.Code != http.StatusOK {
		t.Fatalf("idem status=%d: %s", again.Code, again.Body.String())
	}
	var duplicate struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(again.Body.Bytes(), &duplicate)
	if duplicate.ID != got.ID {
		t.Fatalf("idempotency mismatch: %+v %+v", duplicate, got)
	}
	changed := bytes.ReplaceAll([]byte(validBrowserBody), []byte("Ready"), []byte("Different"))
	conflict := request("org-a", "owner", http.MethodPost, string(changed))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d %s", conflict.Code, conflict.Body.String())
	}
}

func TestBrowserJobAPIRejectsUnauthorizedTargetsAndMutationSteps(t *testing.T) {
	_, request := browserHandlerFixture(t)
	for _, body := range []string{
		`{"idempotency_key":"a","start_url":"http://example.com/","checks":[{"type":"text_visible","value":"Ready"}],"timeout_ms":15000}`,
		`{"idempotency_key":"a","start_url":"https://evil.example.com/","checks":[{"type":"text_visible","value":"Ready"}],"timeout_ms":15000}`,
		`{"idempotency_key":"a","start_url":"https://example.com/","checks":[{"type":"click","value":"#pay"}],"timeout_ms":15000}`,
		`{"idempotency_key":"a","start_url":"https://example.com/","checks":[],"timeout_ms":15000}`,
		`{"idempotency_key":"a","start_url":"https://example.com/","checks":[{"type":"text_visible","value":"Ready"}],"timeout_ms":999999}`,
		`{"idempotency_key":"a","start_url":"https://example.com/","checks":[{"type":"text_visible","value":"Ready"}],"timeout_ms":15000,"tenant_id":"org-b"}`,
	} {
		rec := request("org-a", "owner", http.MethodPost, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("bad job status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func TestBrowserJobAPIGetIsScopedToAuthenticatedOrganization(t *testing.T) {
	h, request := browserHandlerFixture(t)
	first := request("org-a", "owner", http.MethodPost, validBrowserBody)
	if first.Code != http.StatusAccepted {
		t.Fatalf("submit=%d %s", first.Code, first.Body.String())
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	get := func(org, role string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/organizations/"+org+"/browser-jobs/"+result.ID, nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("orgID", org)
		rc.URLParams.Add("id", result.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
		if role != "unauthenticated" {
			req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "user-a", OrgID: "org-a", Role: role}))
		}
		out := httptest.NewRecorder()
		h.Get(out, req)
		return out
	}
	if resp := get("org-a", "member"); resp.Code != http.StatusOK {
		t.Fatalf("get same org=%d %s", resp.Code, resp.Body.String())
	}
	if resp := get("org-b", "owner"); resp.Code != http.StatusNotFound {
		t.Fatalf("get cross org=%d", resp.Code)
	}
	if resp := get("org-a", "unauthenticated"); resp.Code != http.StatusUnauthorized {
		t.Fatalf("get unauth=%d", resp.Code)
	}
}

func TestBrowserJobAPIDisabledWithoutOperatorAllowlist(t *testing.T) {
	h, request := browserHandlerFixture(t)
	h.allowedOrigins = map[string]struct{}{}
	resp := request("org-a", "owner", http.MethodPost, validBrowserBody)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("disabled status=%d %s", resp.Code, resp.Body.String())
	}
}
