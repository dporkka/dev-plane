package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/prfactory"
	"github.com/pressly/goose/v3"
)

func TestReviewedBrokerDurableAuthority(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	remote := filepath.Join(root, "acme", "widget.git")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(remote), 0700); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("/usr/bin/git", args...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Git %s failed: %v %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(source, "init", "--template=", "-b", "main")
	commit := func(msg string) string {
		git(source, "-c", "user.name=fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", msg)
		return git(source, "rev-parse", "HEAD")
	}
	base := commit("base")
	git(root, "clone", "--bare", source, remote)
	git(remote, "config", "http.receivepack", "true")
	head := commit("review")
	git(source, "branch", "export")
	bundle := filepath.Join(root, "export.bundle")
	git(source, "bundle", "create", bundle, "refs/heads/export")
	backend := &cgi.Handler{Path: filepath.Join(git(source, "--exec-path"), "git-http-backend"), Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=fixture"}}
	var calls, mutations, bad, prPosts atomic.Int64
	var remoteHead atomic.Value
	remoteHead.Store(head)
	var prMismatch atomic.Bool
	var foreignPR atomic.Bool
	var staleBase atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/api/v1/repos/acme/widget" {
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "token fixture-api" {
				bad.Add(1)
				http.Error(w, "denied", 401)
				return
			}
			origin := "http://" + r.Host
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 77, "name": "widget", "full_name": "acme/widget", "clone_url": origin + "/acme/widget.git", "html_url": origin + "/acme/widget", "default_branch": "main"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/repos/acme/widget/") {
			if r.Header.Get("Authorization") != "token fixture-api" {
				bad.Add(1)
				http.Error(w, "denied", 401)
				return
			}
			route := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/acme/widget/")
			if strings.HasPrefix(route, "branches/") && r.Method == http.MethodGet {
				branch := strings.TrimPrefix(route, "branches/")
				sha := remoteHead.Load().(string)
				if branch == "main" {
					sha = base
					if staleBase.Load() {
						sha = head
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"name": branch, "commit": map[string]any{"id": sha}})
				return
			}
			if route == "pulls" && r.Method == http.MethodPost {
				prPosts.Add(1)
			} else if route != "pulls/7" || r.Method != http.MethodGet {
				http.NotFound(w, r)
				return
			}
			sha := head
			if prMismatch.Load() {
				sha = base
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 70, "number": 7, "state": "open", "html_url": "http://" + r.Host + "/acme/widget/pulls/7", "head": map[string]any{"ref": "agent/concurrent", "sha": sha, "repo": map[string]any{"id": func() int {
				if foreignPR.Load() {
					return 88
				}
				return 77
			}()}}, "base": map[string]any{"ref": "main", "sha": base, "repo": map[string]any{"id": 77}}})
			return
		}
		if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("writer:fixture-push")) {
			bad.Add(1)
			http.Error(w, "denied", 401)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git-receive-pack") {
			mutations.Add(1)
		}
		backend.ServeHTTP(w, r)
	}))
	defer server.Close()
	for key, value := range map[string]string{
		"GITEA_URL": server.URL, "GITEA_TOKEN": "fixture-api", "GITEA_BINDING_GENERATION": "1", "GITEA_GIT_USERNAME": "writer",
		"GITEA_GIT_PASSWORD": "fixture-push", "GITEA_PUSH_REPOSITORY_ID": "77", "GITEA_PUSH_URL": server.URL + "/acme/widget.git", "GITEA_CLONE_TOKEN": "",
	} {
		t.Setenv(key, value)
	}
	dsn := "file:" + filepath.Join(root, "broker.db")
	if external := os.Getenv("DEVPLANE_TEST_PUSH_POSTGRES_URL"); external != "" {
		u, err := url.Parse(external)
		if err != nil || u.Scheme != "postgres" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User == nil || u.User.String() != "fixture" || u.Path != "/devplane_push_test" || u.RawQuery != "sslmode=disable" || u.Fragment != "" {
			t.Fatal("disposable loopback PostgreSQL required")
		}
		dsn = external
	}
	database, err := db.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	if database.Driver == "postgres" {
		var tables int
		if err := database.QueryRow("SELECT COUNT(*) FROM pg_tables WHERE schemaname='public'").Scan(&tables); err != nil || tables != 0 {
			t.Fatal("fixture database must be empty")
		}
	}
	if err := database.RunMigrations("../../../../packages/db/migrations"); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(database.DB, "../../../../packages/db/migrations", 28); err != nil {
		t.Fatalf("empty evidence migration rollback failed: %v", err)
	}
	if err := database.RunMigrations("../../../../packages/db/migrations"); err != nil {
		t.Fatal(err)
	}
	id := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	org, user, project, repo, task, workspace, run, review, approval := id(1), id(2), id(3), id(4), id(5), id(6), id(7), id(8), id(9)
	execSQL := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	execSQL("INSERT INTO organizations(id,name,slug) VALUES ($1,'fixture','fixture')", org)
	execSQL("INSERT INTO users(id,organization_id,email) VALUES ($1,$2,'fixture@example.test')", user, org)
	execSQL("INSERT INTO projects(id,organization_id,name,slug) VALUES ($1,$2,'fixture','fixture')", project, org)
	execSQL("INSERT INTO repositories(id,project_id,owner,name,full_name,clone_url,settings) VALUES ($1,$2,'acme','widget','acme/widget',$3,$4)", repo, project, server.URL+"/acme/widget.git", fmt.Sprintf(`{"forge":{"provider":"gitea","base_url":%q}}`, server.URL))
	execSQL("INSERT INTO tasks(id,project_id,repository_id,created_by,title,status) VALUES ($1,$2,$3,$4,'fixture','reviewing')", task, project, repo, user)
	execSQL("INSERT INTO workspaces(id,repository_id,task_id,name,branch,base_branch,runtime_provider,status) VALUES ($1,$2,$3,'fixture','agent/fixture','main','docker','ready')", workspace, repo, task)
	execSQL("UPDATE tasks SET workspace_id=$1 WHERE id=$2", workspace, task)
	now := time.Now().UTC().Truncate(time.Second)
	execSQL("INSERT INTO agent_runs(id,task_id,workspace_id,status,completed_at) VALUES ($1,$2,$3,'completed',$4)", run, task, workspace, now)
	execSQL("INSERT INTO review_reports(id,run_id,summary,risk_level,approvable,created_at) VALUES ($1,$2,'fixture','low',TRUE,$3)", review, run, now)
	execSQL("INSERT INTO approvals(id,task_id,agent_run_id,approval_type,requested_by,responded_by,response,responded_at) VALUES ($1,$2,$3,'pr_create',$4,$4,'approved',$5)", approval, task, run, user, now)
	execSQL("INSERT INTO gitea_repository_bindings(repository_id,project_id,organization_id,instance_url,upstream_repository_id,full_name) VALUES ($1,$2,$3,$4,77,'acme/widget')", repo, project, org, server.URL)
	req := prfactory.ReviewedPushRequest{GiteaBindingRequest: prfactory.GiteaBindingRequest{RepositoryID: repo, ProjectID: project, OrganizationID: org, UpstreamRepositoryID: 77}, TaskID: task, RunID: run, ReviewID: review, ApprovalID: approval, Branch: "agent/fixture", BaseBranch: "main", HeadSHA: head, BaseSHA: base, BundlePath: bundle, ExpectedGeneration: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reject := func(r prfactory.ReviewedPushRequest) {
		t.Helper()
		if err := prfactory.PublishReviewedGiteaBundle(ctx, database.DB, r); !errors.Is(err, prfactory.ErrReviewedPush) {
			t.Fatalf("expected authority rejection, got %v", err)
		}
	}
	wrong := req
	wrong.ApprovalID = id(19)
	reject(wrong)
	wrong = req
	wrong.RunID = id(17)
	reject(wrong)
	wrong = req
	wrong.ReviewID = id(18)
	reject(wrong)
	wrong = req
	wrong.OrganizationID = id(11)
	reject(wrong)
	wrong = req
	wrong.Branch = "agent/other"
	reject(wrong)
	wrong = req
	wrong.ExpectedGeneration = 2
	reject(wrong)
	execSQL("UPDATE approvals SET response='rejected' WHERE id=$1", approval)
	reject(req)
	execSQL("UPDATE approvals SET response='approved' WHERE id=$1", approval)
	execSQL("UPDATE review_reports SET approvable=FALSE WHERE id=$1", review)
	reject(req)
	execSQL("UPDATE review_reports SET approvable=TRUE WHERE id=$1", review)
	execSQL("UPDATE gitea_repository_bindings SET revoked_at=$1 WHERE repository_id=$2", now, repo)
	reject(req)
	execSQL("UPDATE gitea_repository_bindings SET revoked_at=NULL WHERE repository_id=$1", repo)
	if calls.Load() != 0 {
		t.Fatal("invalid authority reached forge")
	}
	t.Setenv("GITEA_GIT_PASSWORD", "fixture-api")
	reject(req)
	t.Setenv("GITEA_GIT_PASSWORD", "fixture-push")
	// Missing durable ledger must fail before any Git mutation.
	execSQL("ALTER TABLE gitea_revision_attestations RENAME TO unavailable_attestations")
	before := mutations.Load()
	reject(req)
	if mutations.Load() != before {
		t.Fatal("push occurred without durable intent")
	}
	execSQL("ALTER TABLE unavailable_attestations RENAME TO gitea_revision_attestations")
	if err := prfactory.PublishReviewedGiteaBundle(ctx, database.DB, req); err != nil {
		t.Logf("fixture calls=%d mutations=%d bad=%d", calls.Load(), mutations.Load(), bad.Load())
		t.Fatal(err)
	}
	if mutations.Load() == 0 || bad.Load() != 0 || git(remote, "rev-parse", "refs/heads/agent/fixture") != head || git(remote, "rev-parse", "refs/heads/main") != base {
		t.Fatal("incorrect publication")
	}
	var recordedHead, recordedBase, recordedRun, recordedActor, recordedBranch, observed string
	var recordedGeneration int64
	if err := database.QueryRow(`SELECT a.head_sha,a.base_sha,a.run_id,a.approved_by,a.branch,a.authorization_generation,r.observed_head_sha
      FROM gitea_revision_attestations a JOIN gitea_publication_receipts r ON r.approval_id=a.approval_id
      WHERE a.approval_id=$1`, approval).Scan(&recordedHead, &recordedBase, &recordedRun, &recordedActor, &recordedBranch, &recordedGeneration, &observed); err != nil {
		t.Fatal(err)
	}
	if recordedHead != head || recordedBase != base || recordedRun != run || recordedActor != user || recordedBranch != req.Branch || recordedGeneration != 1 || observed != head {
		t.Fatal("incorrect durable evidence")
	}
	// Even if the remote branch is deleted, the same approval cannot replay.
	git(remote, "update-ref", "-d", "refs/heads/agent/fixture")
	before = mutations.Load()
	reject(req)
	if mutations.Load() != before {
		t.Fatal("consumed approval replayed")
	}
	if refs := git(remote, "for-each-ref", "--format=%(refname)", "refs/heads/agent/fixture"); refs != "" {
		t.Fatal("branch recreated")
	}
	// A new authorized branch is allowed, but a receipt write failure must
	// leave a durable intent and block replay rather than retry the remote.
	approval2 := id(21)
	execSQL("UPDATE workspaces SET branch='agent/uncertain' WHERE id=$1", workspace)
	execSQL("INSERT INTO approvals(id,task_id,agent_run_id,approval_type,requested_by,responded_by,response,responded_at) VALUES ($1,$2,$3,'pr_create',$4,$4,'approved',$5)", approval2, task, run, user, now)
	pending := req
	pending.ApprovalID = approval2
	pending.Branch = "agent/uncertain"
	execSQL("ALTER TABLE gitea_publication_receipts RENAME TO unavailable_receipts")
	reject(pending)
	if got := git(remote, "rev-parse", "refs/heads/agent/uncertain"); got != head {
		t.Fatal("expected remote confirmation before receipt failure")
	}
	execSQL("ALTER TABLE unavailable_receipts RENAME TO gitea_publication_receipts")
	var intentCount, receiptCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM gitea_revision_attestations WHERE approval_id=$1", approval2).Scan(&intentCount); err != nil || intentCount != 1 {
		t.Fatal("pending intent missing", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM gitea_publication_receipts WHERE approval_id=$1", approval2).Scan(&receiptCount); err != nil || receiptCount != 0 {
		t.Fatal("false receipt", err)
	}
	git(remote, "update-ref", "-d", "refs/heads/agent/uncertain")
	before = mutations.Load()
	reject(pending)
	if mutations.Load() != before {
		t.Fatal("pending publication replayed")
	}
	// A different approval cannot reuse a consumed repository/branch.
	approval3 := id(22)
	execSQL("INSERT INTO approvals(id,task_id,agent_run_id,approval_type,requested_by,responded_by,response,responded_at) VALUES ($1,$2,$3,'pr_create',$4,$4,'approved',$5)", approval3, task, run, user, now)
	conflict := pending
	conflict.ApprovalID = approval3
	reject(conflict)
	if mutations.Load() != before {
		t.Fatal("new approval reused consumed branch")
	}
	// Two independent broker database connections race for a fresh intent.
	execSQL("UPDATE workspaces SET branch='agent/concurrent' WHERE id=$1", workspace)
	conflict.Branch = "agent/concurrent"
	otherDB, err := db.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = otherDB.Close() }()
	otherDB.SetMaxOpenConns(1)
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, conn := range []*sql.DB{database.DB, otherDB.DB} {
		go func(conn *sql.DB) {
			<-start
			results <- prfactory.PublishReviewedGiteaBundle(ctx, conn, conflict)
		}(conn)
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, prfactory.ErrReviewedPush) {
			t.Fatal(err)
		}
	}
	if successes != 1 || mutations.Load() != before+1 {
		t.Fatalf("concurrent publication successes=%d mutations=%d", successes, mutations.Load()-before)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM gitea_publication_receipts WHERE approval_id=$1", approval3).Scan(&receiptCount); err != nil || receiptCount != 1 {
		t.Fatal("concurrent receipt missing", err)
	}
	if database.Driver == "postgres" {
		t.Setenv("FORGE_PROVIDER", "gitea")
		factory := prfactory.NewFactory(database.DB, nil)
		// An attestation alone is not publication authority.
		execSQL("ALTER TABLE gitea_publication_receipts RENAME TO unavailable_receipts")
		if _, err := factory.CreatePullRequest(ctx, task); err == nil || prPosts.Load() != 0 {
			t.Fatal("created PR without receipt")
		}
		execSQL("ALTER TABLE unavailable_receipts RENAME TO gitea_publication_receipts")
		remoteHead.Store(base)
		if _, err := factory.CreatePullRequest(ctx, task); err == nil || prPosts.Load() != 0 {
			t.Fatal("created PR for stale head")
		}
		remoteHead.Store(head)
		execSQL("UPDATE approvals SET response='rejected' WHERE id=$1", approval3)
		if _, err := factory.CreatePullRequest(ctx, task); err == nil || prPosts.Load() != 0 {
			t.Fatal("created PR after approval rejection")
		}
		execSQL("UPDATE approvals SET response='approved' WHERE id=$1", approval3)
		pr, err := factory.CreatePullRequest(ctx, task)
		if err != nil || pr == nil || pr.Number != 7 || prPosts.Load() != 1 {
			t.Fatalf("guarded PR creation failed: %v", err)
		}
		if _, err := factory.CreatePullRequest(ctx, task); err == nil || prPosts.Load() != 1 {
			t.Fatal("duplicate PR attempt")
		}
		request := prfactory.GiteaMergePreflightRequest{PullRequestID: pr.ID, RepositoryID: repo, ProjectID: project, OrganizationID: org, ExpectedHeadSHA: head, ExpectedBaseSHA: base, ExpectedGeneration: 1}
		check := func(req prfactory.GiteaMergePreflightRequest) error {
			_, err := prfactory.CheckGiteaMergePreflight(ctx, database.DB, req)
			return err
		}
		if err := check(request); err != nil {
			t.Fatalf("valid merge preflight failed: %v", err)
		}
		wrong := request
		wrong.ExpectedHeadSHA = base
		if err := check(wrong); err == nil {
			t.Fatal("wrong head accepted")
		}
		wrong = request
		wrong.OrganizationID = id(30)
		if err := check(wrong); err == nil {
			t.Fatal("foreign tenant accepted")
		}
		staleBase.Store(true)
		if err := check(request); err == nil {
			t.Fatal("advanced base accepted")
		}
		staleBase.Store(false)
		foreignPR.Store(true)
		if err := check(request); err == nil {
			t.Fatal("forked PR accepted")
		}
		foreignPR.Store(false)
		prMismatch.Store(true)
		if err := check(request); err == nil {
			t.Fatal("changed PR head accepted")
		}
		prMismatch.Store(false)
		execSQL("UPDATE approvals SET response='rejected' WHERE id=$1", approval3)
		if err := check(request); err == nil {
			t.Fatal("revoked approval accepted")
		}
		execSQL("UPDATE approvals SET response='approved' WHERE id=$1", approval3)
		if prPosts.Load() != 1 {
			t.Fatal("preflight mutated upstream")
		}

	}
	// A receipt cannot refer to a different SHA or nonexistent intent.
	if _, err := database.Exec("INSERT INTO gitea_publication_receipts(approval_id,observed_head_sha) VALUES ($1,$2)", approval2, base); err == nil {
		t.Fatal("receipt accepted a mismatched SHA")
	}
	if err := goose.DownTo(database.DB, "../../../../packages/db/migrations", 28); err == nil {
		t.Fatal("migration rollback erased durable evidence")
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM gitea_revision_attestations").Scan(&intentCount); err != nil || intentCount != 3 {
		t.Fatal("rollback changed evidence", intentCount, err)
	}
	reject(req)
	var status sql.NullString
	if err := database.QueryRow("SELECT status FROM tasks WHERE id=$1", task).Scan(&status); err != nil || status.String != map[string]string{"postgres": "pr_created", "sqlite": "reviewing"}[database.Driver] {
		t.Fatal("guarded PR must update task state")
	}
}
