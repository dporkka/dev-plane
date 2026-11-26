package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/prfactory"
	"github.com/pressly/goose/v3"
)

func TestGiteaBindingOwnsNumericIdentity(t *testing.T) {
	driver, dsn := "sqlite3", "file:"+filepath.Join(t.TempDir(), "test.db")+"?_foreign_keys=on"
	if external := os.Getenv("DEVPLANE_TEST_BINDING_POSTGRES_URL"); external != "" {
		parsed, err := url.Parse(external)
		if err != nil || parsed.Scheme != "postgres" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" ||
			parsed.User == nil || parsed.User.String() != "fixture" || parsed.Path != "/devplane_gitea_binding_test" ||
			parsed.RawQuery != "sslmode=disable" || parsed.Fragment != "" {
			t.Fatal("expected disposable loopback binding fixture")
		}
		driver, dsn = "postgres", external
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if driver == "postgres" {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM pg_tables WHERE schemaname='public'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("fixture must be empty: %d %v", count, err)
		}
	}
	if err := goose.SetDialect(driver); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "../../../../packages/db/migrations"); err != nil {
		t.Fatal(err)
	}

	var requests, upstreamID atomic.Int64
	upstreamID.Store(77)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		root := "http://" + r.Host + "/code"
		if r.Method != http.MethodGet || r.URL.Path != "/code/api/v1/repos/acme/widget" ||
			r.Header.Get("Authorization") != "token fixture-api-key" {
			http.Error(w, "unexpected request", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": upstreamID.Load(), "name": "widget", "full_name": "acme/widget", "html_url": root + "/acme/widget",
			"clone_url": root + "/acme/widget.git", "default_branch": "trunk", "private": true,
		})
	}))
	defer srv.Close()
	root := srv.URL + "/code"
	forge, err := gateway.NewGiteaForge(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	cred := gateway.ForgeCredential{AccessToken: "fixture-api-key"}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO organizations(id,name,slug) VALUES ('00000001-0000-4000-8000-000000000001','One','one'),('00000001-0000-4000-8000-000000000002','Two','two')`)
	exec(`INSERT INTO projects(id,organization_id,name,slug) VALUES ('00000002-0000-4000-8000-000000000001','00000001-0000-4000-8000-000000000001','One','one'),('00000002-0000-4000-8000-000000000002','00000001-0000-4000-8000-000000000002','Two','two')`)
	for i := 1; i <= 2; i++ {
		exec(`INSERT INTO repositories(id,project_id,owner,name,full_name,clone_url,settings)
        VALUES ($1,$2,'acme','widget','acme/widget',$3,$4)`,
			fmt.Sprintf("00000003-0000-4000-8000-%012d", i), fmt.Sprintf("00000002-0000-4000-8000-%012d", i), root+"/acme/widget.git",
			fmt.Sprintf(`{"forge":{"provider":"gitea","base_url":%q}}`, root))
	}
	ctx := context.Background()
	grant := prfactory.GiteaBindingRequest{RepositoryID: "00000003-0000-4000-8000-000000000001", ProjectID: "00000002-0000-4000-8000-000000000001", OrganizationID: "00000001-0000-4000-8000-000000000001", UpstreamRepositoryID: 77}
	verify := func(repo, project string) error {
		_, err := prfactory.VerifyGiteaRepositoryBinding(ctx, db, forge, cred, repo, project)
		return err
	}
	requireRejected := func(err error) {
		t.Helper()
		if !errors.Is(err, prfactory.ErrGiteaRepositoryBinding) {
			t.Fatalf("expected binding rejection, got %v", err)
		}
	}
	requireRejected(verify("00000003-0000-4000-8000-000000000001", "00000002-0000-4000-8000-000000000001"))
	wrong := grant
	wrong.OrganizationID = "00000001-0000-4000-8000-000000000002"
	_, err = prfactory.BindGiteaRepository(ctx, db, forge, cred, wrong)
	requireRejected(err)
	if requests.Load() != 0 {
		t.Fatal("unbound or foreign tenant reached upstream")
	}

	binding, err := prfactory.BindGiteaRepository(ctx, db, forge, cred, grant)
	if err != nil || binding.UpstreamRepositoryID != 77 {
		t.Fatalf("bind failed: %+v %v", binding, err)
	}
	if _, err := prfactory.BindGiteaRepository(ctx, db, forge, cred, grant); err != nil {
		t.Fatalf("repeat grant: %v", err)
	}
	if err := verify("00000003-0000-4000-8000-000000000001", "00000002-0000-4000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}

	var status, branch string
	var private bool
	if err := db.QueryRow(`SELECT connection_status,default_branch,private FROM repositories WHERE id='00000003-0000-4000-8000-000000000001'`).Scan(&status, &branch, &private); err != nil {
		t.Fatal(err)
	}
	if status != "connected" || branch != "trunk" || !private {
		t.Fatalf("unexpected verified state %s %s %v", status, branch, private)
	}
	calls := requests.Load()
	requireRejected(verify("00000003-0000-4000-8000-000000000001", "00000002-0000-4000-8000-000000000002"))
	requireRejected(verify("00000003-0000-4000-8000-000000000002", "00000002-0000-4000-8000-000000000002"))
	if requests.Load() != calls {
		t.Fatal("foreign project reached upstream")
	}

	other := prfactory.GiteaBindingRequest{RepositoryID: "00000003-0000-4000-8000-000000000002", ProjectID: "00000002-0000-4000-8000-000000000002", OrganizationID: "00000001-0000-4000-8000-000000000002", UpstreamRepositoryID: 77}
	_, err = prfactory.BindGiteaRepository(ctx, db, forge, cred, other)
	requireRejected(err)
	if err := db.QueryRow("SELECT connection_status FROM repositories WHERE id='00000003-0000-4000-8000-000000000002'").Scan(&status); err != nil || status != "pending" {
		t.Fatalf("foreign repository status=%q err=%v", status, err)
	}

	upstreamID.Store(88)
	requireRejected(verify("00000003-0000-4000-8000-000000000001", "00000002-0000-4000-8000-000000000001"))
	wrong = grant
	wrong.UpstreamRepositoryID = 88
	_, err = prfactory.BindGiteaRepository(ctx, db, forge, cred, wrong)
	requireRejected(err)
	upstreamID.Store(77)
	exec("UPDATE projects SET organization_id='00000001-0000-4000-8000-000000000002' WHERE id='00000002-0000-4000-8000-000000000001'")
	calls = requests.Load()
	requireRejected(verify("00000003-0000-4000-8000-000000000001", "00000002-0000-4000-8000-000000000001"))
	if requests.Load() != calls {
		t.Fatal("changed tenant reached upstream")
	}
	exec("UPDATE projects SET organization_id='00000001-0000-4000-8000-000000000001' WHERE id='00000002-0000-4000-8000-000000000001'")
	exec("UPDATE repositories SET deleted_at=CURRENT_TIMESTAMP WHERE id='00000003-0000-4000-8000-000000000001'")
	requireRejected(verify("00000003-0000-4000-8000-000000000001", "00000002-0000-4000-8000-000000000001"))
	_, err = prfactory.BindGiteaRepository(ctx, db, forge, cred, other)
	requireRejected(err)
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM gitea_repository_bindings").Scan(&count); err != nil || count != 1 {
		t.Fatalf("binding count=%d err=%v", count, err)
	}
}
