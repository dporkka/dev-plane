package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/api/internal/auth"
	appmiddleware "github.com/ai-dev-control-plane/api/internal/middleware"
	"github.com/go-chi/chi/v5"
	_ "github.com/lib/pq"
)

const (
	isolateOrgA  = "org-A"
	isolateOrgB  = "org-B"
	isolateRepoA = "11111111-1111-4111-8111-111111111111"
	isolateRepoB = "22222222-2222-4222-8222-222222222222"
)

// Opt-in: DEV_PLANE_TEST_DATABASE_URL=postgres://localhost:55439/postgres?sslmode=disable
// Uses TEMP tables on a single local connection; never modifies persistent tables.
func TestReviewScopePostgresIsolation(t *testing.T) {
	dsn := os.Getenv("DEV_PLANE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated local PostgreSQL via DEV_PLANE_TEST_DATABASE_URL")
	}
	if !strings.Contains(dsn, "localhost") && !strings.Contains(dsn, "127.0.0.1") {
		t.Fatal("refusing non-local PostgreSQL database for isolation test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}

	statements := []string{
		`CREATE TEMP TABLE users (id text PRIMARY KEY, organization_id text NOT NULL, deleted_at timestamptz)`,
		`CREATE TEMP TABLE projects (id text PRIMARY KEY, organization_id text NOT NULL, deleted_at timestamptz)`,
		`CREATE TEMP TABLE repositories (id uuid PRIMARY KEY, project_id text NOT NULL, deleted_at timestamptz)`,
		`CREATE TEMP TABLE tasks (id text PRIMARY KEY, project_id text NOT NULL, deleted_at timestamptz)`,
		`CREATE TEMP TABLE agent_runs (id text PRIMARY KEY, task_id text NOT NULL)`,
		`CREATE TEMP TABLE pull_requests (
   id text PRIMARY KEY, task_id text, run_id text, repository_id uuid NOT NULL, number integer,
   title text, body text, branch text, base_branch text, url text, state text,
   draft bool, created_by text, merged_at timestamptz, created_at timestamptz, updated_at timestamptz
   )`,
		`CREATE TEMP TABLE review_reports (
   id text PRIMARY KEY, run_id text NOT NULL, summary text NOT NULL, findings jsonb,
   risk_level text, approvable bool, suggestions jsonb, test_coverage text,
   security_notes text, diff_summary jsonb, created_at timestamptz
  )`,
		`INSERT INTO users(id, organization_id) VALUES ('user-A','org-A'),('user-B','org-B')`,
		`INSERT INTO projects(id, organization_id) VALUES ('project-A','org-A'),('project-B','org-B')`,
		`INSERT INTO repositories(id, project_id) VALUES
    ('11111111-1111-4111-8111-111111111111','project-A'),
    ('22222222-2222-4222-8222-222222222222','project-B')`,
		`INSERT INTO tasks(id, project_id) VALUES ('task-A','project-A'),('task-B','project-B')`,
		`INSERT INTO agent_runs(id, task_id) VALUES ('run-A','task-A'),('run-B','task-B')`,
		`INSERT INTO pull_requests
   (id,task_id,run_id,repository_id,number,title,body,branch,base_branch,url,state,draft,created_by,created_at,updated_at)
   VALUES
    ('pr-A','task-A','run-A','11111111-1111-4111-8111-111111111111',101,'Alpha PR','','alpha','main','https://github.com/example/a/pull/101','open',false,'user-A','2026-11-18T10:00:00Z','2026-11-18T10:00:00Z'),
    ('pr-B','task-B','run-B','22222222-2222-4222-8222-222222222222',102,'Beta PR','','beta','main','https://github.com/example/b/pull/102','open',false,'user-B','2026-11-18T10:00:00Z','2026-11-18T10:00:00Z')`,
		`INSERT INTO review_reports
   (id,run_id,summary,findings,risk_level,approvable,suggestions,test_coverage,security_notes,diff_summary,created_at)
   VALUES
    ('review-A','run-A','Alpha report','[{"severity":"critical","category":"security","file":"a","line":1,"message":"alpha only"}]'::jsonb,'critical',false,'[]','ok','reviewed','{}','2026-11-19T10:00:00Z'),
    ('review-B','run-B','Beta report','[{"severity":"high","category":"security","file":"b","line":2,"message":"beta only"}]'::jsonb,'high',false,'[]','ok','reviewed','{}','2026-11-19T10:00:00Z')`,
	}
	for i, query := range statements {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("fixture SQL %d: %v", i, err)
		}
	}

	h := NewHandler(db, slog.Default())
	secret := "local-isolation-jwt-secret-for-integration-tests"
	router := chi.NewRouter()
	router.Use(appmiddleware.Auth(secret, slog.Default()))
	router.Get("/organizations/{orgID}/review-metrics", h.GetReviewMetrics)
	router.Get("/pull-requests/{id}", h.GetPullRequest)
	router.Get("/runs/{runId}/review", h.GetReview)
	asUser := func(who, org, url string) *httptest.ResponseRecorder {
		t.Helper()
		token, err := auth.GenerateToken(who, org, who+"@example.invalid", "member", secret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/organizations/org-A/review-metrics?start=2026-11-01&end=2026-11-30", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request status=%d want 401", unauthenticated.Code)
	}
	metricsURL := func(org string, repo string) string {
		value := fmt.Sprintf("/organizations/%s/review-metrics?start=2026-11-01&end=2026-11-30", org)
		if repo != "" {
			value += "&repository=" + repo
		}
		return value
	}
	checkMetrics := func(who, org, reqOrg, repo string, wantStatus int, wantCount, critical, high int64) {
		t.Helper()
		rec := asUser(who, org, metricsURL(reqOrg, repo))
		if rec.Code != wantStatus {
			t.Fatalf("metrics %s to %s status=%d want=%d body=%s", who, reqOrg, rec.Code, wantStatus, rec.Body.String())
		}
		if wantStatus != http.StatusOK {
			return
		}
		var m ReviewMetrics
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if m.ReviewedPRs != wantCount || m.CriticalFindings != critical || m.HighFindings != high {
			t.Fatalf("metrics %s to %s unexpected %+v", who, reqOrg, m)
		}
	}
	checkMetrics("user-A", isolateOrgA, isolateOrgA, "", 200, 1, 1, 0)
	checkMetrics("user-A", isolateOrgA, isolateOrgA, isolateRepoB, 200, 0, 0, 0)
	checkMetrics("user-A", isolateOrgA, isolateOrgB, "", 404, 0, 0, 0)
	checkMetrics("user-B", isolateOrgB, isolateOrgB, "", 200, 1, 0, 1)
	checkMetrics("user-B", isolateOrgB, isolateOrgA, "", 404, 0, 0, 0)

	for _, tc := range []struct {
		user, org, id string
		want          int
	}{
		{"user-A", isolateOrgA, "pr-A", 200},
		{"user-A", isolateOrgA, "pr-B", 404},
		{"user-B", isolateOrgB, "pr-A", 404},
		{"user-B", isolateOrgB, "pr-B", 200},
	} {
		rec := asUser(tc.user, tc.org, "/pull-requests/"+tc.id)
		if rec.Code != tc.want {
			t.Fatalf("GetPullRequest %s %s status=%d want=%d body=%s", tc.user, tc.id, rec.Code, tc.want, rec.Body.String())
		}
	}
	for _, tc := range []struct {
		user, org, id string
		want          int
	}{
		{"user-A", isolateOrgA, "run-A", 200},
		{"user-A", isolateOrgA, "run-B", 404},
		{"user-B", isolateOrgB, "run-A", 404},
		{"user-B", isolateOrgB, "run-B", 200},
	} {
		rec := asUser(tc.user, tc.org, "/runs/"+tc.id+"/review")
		if rec.Code != tc.want {
			t.Fatalf("GetReview %s %s status=%d want=%d body=%s", tc.user, tc.id, rec.Code, tc.want, rec.Body.String())
		}
	}
}
