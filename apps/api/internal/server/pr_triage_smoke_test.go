package server

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/config"
	_ "github.com/mattn/go-sqlite3"
)

// TestExternalPRTriageAuthenticatedHTTP is a self-contained local staging smoke:
// real router, real bearer middleware, real database, real HTTP requests.
func TestExternalPRTriageAuthenticatedHTTP(t *testing.T) {
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)

	_, err = database.Exec(`
        CREATE TABLE organizations (id TEXT PRIMARY KEY);
        CREATE TABLE users (id TEXT PRIMARY KEY, organization_id TEXT NOT NULL, role TEXT NOT NULL, deleted_at TIMESTAMP);
        CREATE TABLE projects (id TEXT PRIMARY KEY, organization_id TEXT NOT NULL, deleted_at TIMESTAMP);
        CREATE TABLE repositories (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, full_name TEXT NOT NULL, deleted_at TIMESTAMP);
        INSERT INTO organizations (id) VALUES ('org-1'), ('org-2');
        INSERT INTO users (id,organization_id,role) VALUES ('owner-1','org-1','owner'),('member-1','org-1','member');
        INSERT INTO projects (id,organization_id) VALUES ('project-1','org-1');
        INSERT INTO repositories (id,project_id,full_name) VALUES ('repo-1','project-1','owner/repo');
    `)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../../packages/db/migrations/026_external_pr_triage.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(strings.Split(string(migration), "-- +goose StatementBegin")[1], "-- +goose StatementEnd")[0]
	if _, err := database.Exec(up); err != nil {
		t.Fatal(err)
	}

	secret := "local-smoke-secret-32-character-minimum"
	srv := New(&config.Config{JWTSecret: secret, AllowedOrigins: []string{"http://localhost:3000"}}, database, slog.Default())
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	token := func(id, role string) string {
		s, err := auth.GenerateToken(id, "org-1", "smoke@example.test", role, secret, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	client := httpSrv.Client()
	send := func(method, path, bearer, body string) (int, string) {
		req, err := http.NewRequest(method, httpSrv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(payload)
	}
	path := "/api/v1/organizations/org-1/pr-triage"
	snapshot := fmt.Sprintf(`{"generated_at":%q,"decisions":[{"pr":{"repo":"owner/repo","number":7,"title":"Staging smoke PR","url":"https://github.com/owner/repo/pull/7","headRefOid":%q,"baseRefName":"main"},"bucket":"review","score":22}]}`,
		time.Now().UTC().Format(time.RFC3339), strings.Repeat("a", 40))

	if code, _ := send(http.MethodGet, path, "", ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read = %d", code)
	}
	if code, _ := send(http.MethodPost, path+"/import", token("member-1", "member"), snapshot); code != http.StatusForbidden {
		t.Fatalf("member import = %d", code)
	}
	if code, _ := send(http.MethodGet, "/api/v1/organizations/org-2/pr-triage", token("member-1", "member"), ""); code != http.StatusNotFound {
		t.Fatalf("cross-org read = %d", code)
	}
	if code, body := send(http.MethodPost, path+"/import", token("owner-1", "owner"), snapshot); code != http.StatusOK {
		t.Fatalf("owner import = %d %s", code, body)
	}
	if code, body := send(http.MethodGet, path+"?bucket=review", token("member-1", "member"), ""); code != http.StatusOK || !strings.Contains(body, "Staging smoke PR") {
		t.Fatalf("member read = %d %s", code, body)
	}
	if _, err := database.Exec("UPDATE users SET role='member' WHERE id='owner-1'"); err != nil {
		t.Fatal(err)
	}
	if code, _ := send(http.MethodPost, path+"/import", token("owner-1", "owner"), snapshot); code != http.StatusForbidden {
		t.Fatalf("demoted owner import = %d", code)
	}
}
