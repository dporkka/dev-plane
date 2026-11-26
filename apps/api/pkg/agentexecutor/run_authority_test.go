package agentexecutor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ai-dev-control-plane/prfactory"
	"github.com/ai-dev-control-plane/runtimes"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecuteRunRejectsForeignProjectAndCredentialBearingLegacyRun(t *testing.T) {
	for _, mode := range []string{"foreign-project", "deleted-project", "mixed-worker"} {
		t.Run(mode, func(t *testing.T) {
			db := setupDispatchDB(t, "{}")
			defer db.Close()
			switch mode {
			case "foreign-project":
				if _, err := db.Exec("UPDATE tasks SET project_id='unrelated'"); err != nil {
					t.Fatal(err)
				}
			case "deleted-project":
				if _, err := db.Exec("UPDATE projects SET deleted_at='deleted'"); err != nil {
					t.Fatal(err)
				}
			case "mixed-worker":
				t.Setenv("GITEA_TOKEN", "synthetic-operator-token")
			}
			legacy := &fakeLegacyRunBackend{}
			e := &Executor{db: db, legacy: legacy}
			if err := e.ExecuteRun(context.Background(), "run-1"); err == nil || legacy.runID != "" {
				t.Fatalf("insecure dispatch: %v", err)
			}
		})
	}
}

func giteaRunFixture(t *testing.T, db *sql.DB, root string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GITEA_BINDING_GENERATION", "1")
	t.Setenv("GITEA_URL", root)
	t.Setenv("GITEA_TOKEN", "fixture-api-key")
	exec(`UPDATE repositories SET owner='acme',name='widget',full_name='acme/widget',clone_url=$1,settings=$2`, root+"/acme/widget.git", fmt.Sprintf(`{"forge":{"provider":"gitea","base_url":%q}}`, root))
	exec("UPDATE agent_runs SET workspace_id='workspace-1'")
	exec("UPDATE tasks SET workspace_id='workspace-1'")
	exec(`INSERT INTO workspaces VALUES('workspace-1','task-1','repo-1','docker','local-123',NULL,'ready',NULL)`)
}

func TestGiteaDispatchRejectsUnboundForeignAndExternalWorkspaces(t *testing.T) {
	for _, mode := range []string{"unbound", "foreign-workspace", "local", "external", "deleted-workspace", "host-path"} {
		t.Run(mode, func(t *testing.T) {
			db := setupDispatchDB(t, "{}")
			defer db.Close()
			giteaRunFixture(t, db, "http://127.0.0.1:1")
			queries := map[string]string{
				"foreign-workspace": "UPDATE workspaces SET task_id='other'",
				"local":             "UPDATE workspaces SET runtime_provider='local'",
				"external":          `UPDATE agent_runs SET metadata='{"execution":{"backend":"agent_runtime"}}'`,
				"deleted-workspace": "UPDATE workspaces SET deleted_at='deleted'",
				"host-path":         "UPDATE workspaces SET worktree_path='/host/worktree'",
			}
			if q := queries[mode]; q != "" {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			legacy, external := &fakeLegacyRunBackend{}, &fakeExternalRunBackend{}
			e := &Executor{db: db, legacy: legacy, external: external}
			err := e.ExecuteRun(context.Background(), "run-1")
			if err == nil || legacy.runID != "" || external.runID != "" {
				t.Fatalf("unsafe dispatch: %v", err)
			}
		})
	}
}

func TestGiteaDispatchRevalidatesUpstreamAndContainerIdentity(t *testing.T) {
	db := setupDispatchDB(t, "{}")
	defer db.Close()
	var id atomic.Int64
	id.Store(77)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "token fixture-api-key" || r.URL.Path != "/api/v1/repos/acme/widget" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		root := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id.Load(), "name": "widget", "full_name": "acme/widget", "html_url": root + "/acme/widget", "clone_url": root + "/acme/widget.git", "default_branch": "main"})
	}))
	defer server.Close()
	giteaRunFixture(t, db, server.URL)
	if _, err := db.Exec("INSERT INTO gitea_repository_bindings(repository_id,project_id,organization_id,instance_url,upstream_repository_id,full_name) VALUES ('repo-1','project-1','org-1',$1,77,'acme/widget')", server.URL); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	authority, err := loadRunAuthority(ctx, db, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	verify := func(_ context.Context, session string, grant runtimes.GiteaWorkspaceGrant) error {
		checked = true
		if session != "local-123" || grant.RepositoryID != "repo-1" || grant.WorkspaceID != "workspace-1" || grant.UpstreamRepositoryID != 77 || grant.AuthorizationGeneration != 1 || grant.InstanceURL != server.URL {
			t.Fatal("incorrect container grant")
		}
		return nil
	}
	if err := authority.authorize(ctx, db, executionBackendLegacy, verify); err != nil || !checked || calls.Load() != 1 {
		t.Fatalf("valid grant not checked: %v", err)
	}
	checked = false
	id.Store(99)
	if err := authority.authorize(ctx, db, executionBackendLegacy, verify); !errors.Is(err, prfactory.ErrGiteaRepositoryBinding) || checked {
		t.Fatalf("recreated repo authorized: %v", err)
	}
	id.Store(77)
	if err := authority.authorize(ctx, db, executionBackendLegacy, func(context.Context, string, runtimes.GiteaWorkspaceGrant) error {
		return runtimes.ErrGiteaWorkspaceAuthority
	}); !errors.Is(err, runtimes.ErrGiteaWorkspaceAuthority) {
		t.Fatalf("failed container verification ignored: %v", err)
	}
}

func TestGiteaRunAuthorityCancelsOnRevocation(t *testing.T) {
	db := setupDispatchDB(t, "{}")
	defer db.Close()
	root := "http://127.0.0.1:1"
	giteaRunFixture(t, db, root)
	if _, err := db.Exec("INSERT INTO gitea_repository_bindings(repository_id,project_id,organization_id,instance_url,upstream_repository_id,full_name) VALUES ('repo-1','project-1','org-1',$1,77,'acme/widget')", root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, err := loadRunAuthority(ctx, db, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	err = a.runWithGiteaAuthority(ctx, db, 1, 5*time.Millisecond, func(runCtx context.Context) error {
		if _, err := db.Exec("UPDATE gitea_repository_bindings SET revoked_at='revoked',authorization_generation=2"); err != nil {
			return err
		}
		<-runCtx.Done()
		return nil // a cooperative runner need not return an error
	})
	if !errors.Is(err, prfactory.ErrGiteaRepositoryBinding) || ctx.Err() != nil {
		t.Fatalf("revocation did not cancel run: %v", err)
	}
	if _, err := db.Exec("UPDATE gitea_repository_bindings SET revoked_at=NULL,authorization_generation=3"); err != nil {
		t.Fatal(err)
	}
	started := false
	err = a.runWithGiteaAuthority(ctx, db, 1, 5*time.Millisecond, func(context.Context) error {
		started = true
		return nil
	})
	if !errors.Is(err, prfactory.ErrGiteaRepositoryBinding) || started {
		t.Fatalf("stale generation launched: %v", err)
	}
}
