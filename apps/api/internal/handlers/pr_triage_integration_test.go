package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
)

func TestPRTriageSQLiteImportAndReadRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
 CREATE TABLE organizations (id TEXT PRIMARY KEY);
 CREATE TABLE users (id TEXT PRIMARY KEY, organization_id TEXT NOT NULL, role TEXT NOT NULL, deleted_at TIMESTAMP);
 CREATE TABLE projects (id TEXT PRIMARY KEY, organization_id TEXT NOT NULL, deleted_at TIMESTAMP);
 CREATE TABLE repositories (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, full_name TEXT NOT NULL, deleted_at TIMESTAMP);
 INSERT INTO organizations (id) VALUES ('org-1');
 INSERT INTO users (id, organization_id, role) VALUES ('user-1', 'org-1', 'owner');
 INSERT INTO projects (id, organization_id) VALUES ('p1', 'org-1');
 INSERT INTO repositories (id, project_id, full_name) VALUES ('r1', 'p1', 'owner/repo');
 `)
	if err != nil {
		t.Fatal(err)
	}

	migration, err := os.ReadFile("../../../../packages/db/migrations/026_external_pr_triage.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose StatementBegin")
	if len(parts) < 2 {
		t.Fatal("missing migration")
	}
	up := strings.Split(parts[1], "-- +goose StatementEnd")[0]
	if _, err := db.Exec(up); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(db, nil)
	created := time.Now().UTC().Truncate(time.Second)
	makeSnapshot := func(title string, timestamp time.Time) string {
		return fmt.Sprintf(`{"generated_at":%q,"decisions":[{"pr":{"repo":"owner/repo","number":7,"title":%q,"url":"https://github.com/owner/repo/pull/7","headRefOid":%q,"baseRefName":"main"},"bucket":"review","score":30,"reasons":["needs review"]}]}`,
			timestamp.Format(time.RFC3339), title, strings.Repeat("a", 40))
	}
	first := httptest.NewRecorder()
	h.ImportPRTriage(first, triageRequest(http.MethodPost, makeSnapshot("New title", created), "owner"))
	if first.Code != 200 || !strings.Contains(first.Body.String(), `"imported":1`) {
		t.Fatalf("insert: %d %s", first.Code, first.Body.String())
	}

	// Importing a delayed older scan must not overwrite a newer snapshot.
	stale := httptest.NewRecorder()
	h.ImportPRTriage(stale, triageRequest(http.MethodPost, makeSnapshot("Old title", created.Add(-time.Hour)), "owner"))
	if stale.Code != 200 || !strings.Contains(stale.Body.String(), `"imported":0`) {
		t.Fatalf("stale import: %d %s", stale.Code, stale.Body.String())
	}

	// Regression: an earlier instant with a positive timezone offset must not
	// replace newer UTC evidence when SQLite compares stored TEXT timestamps.
	staleOffset := httptest.NewRecorder()
	olderWithOffset := created.Add(-30 * time.Minute).In(time.FixedZone("UTC+2", 2*60*60))
	h.ImportPRTriage(staleOffset, triageRequest(http.MethodPost, makeSnapshot("Offset older title", olderWithOffset), "owner"))
	if staleOffset.Code != http.StatusOK || !strings.Contains(staleOffset.Body.String(), `"imported":0`) {
		t.Fatalf("offset stale import: %d %s", staleOffset.Code, staleOffset.Body.String())
	}

	get := func() (int, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/organizations/org-1/pr-triage", nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("orgID", testOrgID)
		req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, rc)))
		h.ListPRTriage(rec, req)
		var response map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return rec.Code, response
	}
	code, data := get()
	if code != 200 {
		t.Fatalf("read: %d %v", code, data)
	}
	items := data["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "New title" {
		t.Fatalf("unexpected imported PRs: %v", items)
	}

	// A previously issued owner JWT must not permit imports after role demotion.
	if _, err := db.Exec("UPDATE users SET role='member' WHERE id='user-1'"); err != nil {
		t.Fatal(err)
	}
	demoted := httptest.NewRecorder()
	h.ImportPRTriage(demoted, triageRequest(http.MethodPost, makeSnapshot("Blocked update", created.Add(time.Minute)), "owner"))
	if demoted.Code != http.StatusForbidden {
		t.Fatalf("demoted owner imported snapshot: %d %s", demoted.Code, demoted.Body.String())
	}

	// Revoking the connected repository hides its prior cached decisions.
	if _, err := db.Exec("UPDATE repositories SET deleted_at = CURRENT_TIMESTAMP WHERE id='r1'"); err != nil {
		t.Fatal(err)
	}
	code, data = get()
	if code != 200 || len(data["items"].([]any)) != 0 {
		t.Fatalf("disconnected repository still visible: %d %v", code, data)
	}
}
