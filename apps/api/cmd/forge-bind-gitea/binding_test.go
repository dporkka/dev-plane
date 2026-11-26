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
	"time"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/prfactory"
	"github.com/pressly/goose/v3"
)

func TestGiteaBindingOwnsNumericIdentity(t *testing.T) {
	t.Setenv("GITEA_BINDING_GENERATION", "1")
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
	var expectedToken atomic.Value
	expectedToken.Store("fixture-api-key")
	var blockLookup atomic.Bool
	lookupStarted, releaseLookup := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		root := "http://" + r.Host + "/code"
		if r.Method != http.MethodGet || r.URL.Path != "/code/api/v1/repos/acme/widget" ||
			r.Header.Get("Authorization") != "token "+expectedToken.Load().(string) {
			http.Error(w, "unexpected request", http.StatusForbidden)
			return
		}
		if blockLookup.Load() {
			close(lookupStarted)
			select {
			case <-releaseLookup:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": upstreamID.Load(), "name": "widget", "full_name": "acme/widget", "html_url": root + "/acme/widget",
			"clone_url": root + "/acme/widget.git", "default_branch": "trunk", "private": true,
		})
	}))
	defer srv.Close()
	defer func() {
		if blockLookup.Load() {
			close(releaseLookup)
		}
	}()
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
	if err != nil || binding.UpstreamRepositoryID != 77 || binding.AuthorizationGeneration != 1 {
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
	// A pre-lifecycle rollback must be safe; after a transition it is forbidden.
	if err := goose.Down(db, "../../../../packages/db/migrations"); err != nil {
		t.Fatalf("pre-lifecycle rollback failed: %v", err)
	}
	if err := goose.Up(db, "../../../../packages/db/migrations"); err != nil {
		t.Fatalf("reapply lifecycle migration failed: %v", err)
	}
	calls = requests.Load()
	for _, malformed := range []string{"", "01", "0", "-1", " 1", "9223372036854775808"} {
		t.Setenv("GITEA_BINDING_GENERATION", malformed)
		requireRejected(verify(grant.RepositoryID, grant.ProjectID))
	}
	if requests.Load() != calls {
		t.Fatal("invalid configuration reached provider")
	}
	t.Setenv("GITEA_BINDING_GENERATION", "1")
	// Emergency revoke is local-only and reserves the upstream identity.
	lifecycle := prfactory.GiteaBindingLifecycleRequest{GiteaBindingRequest: grant, InstanceURL: root, ExpectedGeneration: 1, Reason: "rotate pilot credentials"}
	calls = requests.Load()
	bad := lifecycle
	bad.OrganizationID = other.OrganizationID
	_, err = prfactory.RevokeGiteaRepositoryBinding(ctx, db, bad)
	requireRejected(err)
	gen, err := prfactory.RevokeGiteaRepositoryBinding(ctx, db, lifecycle)
	if err != nil || gen != 2 {
		t.Fatalf("revoke generation=%d err=%v", gen, err)
	}
	_, err = prfactory.RevokeGiteaRepositoryBinding(ctx, db, lifecycle)
	requireRejected(err)
	requireRejected(verify(grant.RepositoryID, grant.ProjectID))
	requireRejected(prfactory.CheckGiteaRepositoryBindingGeneration(ctx, db, grant.RepositoryID, grant.ProjectID, 1))
	_, err = prfactory.BindGiteaRepository(ctx, db, forge, cred, grant)
	requireRejected(err)
	if requests.Load() != calls {
		t.Fatal("revoked/stale authority touched upstream")
	}
	if err := db.QueryRow("SELECT connection_status FROM repositories WHERE id=$1", grant.RepositoryID).Scan(&status); err != nil || status != "disconnected" {
		t.Fatalf("revoked status=%s %v", status, err)
	}
	// The failed down must preserve the tombstone and the audit record.
	if err := goose.Down(db, "../../../../packages/db/migrations"); err == nil {
		t.Fatal("rollback discarded revocation history")
	}
	lifecycle.ExpectedGeneration = 2
	t.Setenv("GITEA_BINDING_GENERATION", "2")
	requireRejected(verify(grant.RepositoryID, grant.ProjectID))
	_, err = prfactory.ReactivateGiteaRepositoryBinding(ctx, db, forge, cred, lifecycle)
	requireRejected(err)
	if requests.Load() != calls {
		t.Fatal("revoked generation touched upstream")
	}

	// Simulate operator rotation: old token is rejected by the upstream.
	t.Setenv("GITEA_BINDING_GENERATION", "3")
	expectedToken.Store("rotated-fixture-api-key")
	_, err = prfactory.ReactivateGiteaRepositoryBinding(ctx, db, forge, cred, lifecycle)
	requireRejected(err)
	cred.AccessToken = "rotated-fixture-api-key"
	upstreamID.Store(88)
	_, err = prfactory.ReactivateGiteaRepositoryBinding(ctx, db, forge, cred, lifecycle)
	requireRejected(err)
	upstreamID.Store(77)
	reactivated, err := prfactory.ReactivateGiteaRepositoryBinding(ctx, db, forge, cred, lifecycle)
	if err != nil || reactivated.AuthorizationGeneration != 3 {
		t.Fatalf("reactivation failed: %+v %v", reactivated, err)
	}
	_, err = prfactory.ReactivateGiteaRepositoryBinding(ctx, db, forge, cred, lifecycle)
	requireRejected(err)
	if err := db.QueryRow("SELECT connection_status FROM repositories WHERE id=$1", grant.RepositoryID).Scan(&status); err != nil || status != "connected" {
		t.Fatalf("reactivated status=%s %v", status, err)
	}
	calls = requests.Load()
	t.Setenv("GITEA_BINDING_GENERATION", "1")
	requireRejected(verify(grant.RepositoryID, grant.ProjectID))
	_, err = prfactory.BindGiteaRepository(ctx, db, forge, cred, grant)
	requireRejected(err)
	if requests.Load() != calls {
		t.Fatal("old generation touched upstream after reactivation")
	}
	t.Setenv("GITEA_BINDING_GENERATION", "3")
	if err := verify(grant.RepositoryID, grant.ProjectID); err != nil {
		t.Fatal(err)
	}
	if err := prfactory.CheckGiteaRepositoryBindingGeneration(ctx, db, grant.RepositoryID, grant.ProjectID, 3); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := db.QueryRow("SELECT COUNT(*) FROM gitea_binding_lifecycle_events WHERE repository_id=$1 AND ((authorization_generation=2 AND action='revoke') OR (authorization_generation=3 AND action='reactivate')) AND reason=$2", grant.RepositoryID, lifecycle.Reason).Scan(&events); err != nil || events != 2 {
		t.Fatalf("lifecycle audit count=%d err=%v", events, err)
	}
	// A provider read started before revocation must not authorize after it.
	blockLookup.Store(true)
	verification := make(chan error, 1)
	go func() { verification <- verify(grant.RepositoryID, grant.ProjectID) }()
	select {
	case <-lookupStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("verification did not reach fixture")
	}
	lifecycle.ExpectedGeneration = 3
	gen, err = prfactory.RevokeGiteaRepositoryBinding(ctx, db, lifecycle)
	if err != nil || gen != 4 {
		t.Fatalf("revoke during provider read: %d %v", gen, err)
	}
	close(releaseLookup)
	blockLookup.Store(false)
	select {
	case err := <-verification:
		requireRejected(err)
	case <-time.After(5 * time.Second):
		t.Fatal("verification did not complete")
	}
	exec("UPDATE repositories SET deleted_at=CURRENT_TIMESTAMP WHERE id='00000003-0000-4000-8000-000000000001'")
	requireRejected(verify("00000003-0000-4000-8000-000000000001", "00000002-0000-4000-8000-000000000001"))
	t.Setenv("GITEA_BINDING_GENERATION", "1")
	_, err = prfactory.BindGiteaRepository(ctx, db, forge, cred, other)
	requireRejected(err)
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM gitea_repository_bindings").Scan(&count); err != nil || count != 1 {
		t.Fatalf("binding count=%d err=%v", count, err)
	}
}
