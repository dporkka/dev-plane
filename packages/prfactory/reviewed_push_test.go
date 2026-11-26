package prfactory

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewedPushCreatesOnlyApprovedRevision(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git %s: %v %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	src := filepath.Join(root, "source")
	remote := filepath.Join(root, "acme", "widget.git")
	if err := os.MkdirAll(src, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(remote), 0700); err != nil {
		t.Fatal(err)
	}
	run(src, "init", "-b", "main", "--template=")
	commit := func(message string) string {
		t.Helper()
		run(src, "-c", "user.name=fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", message)
		return run(src, "rev-parse", "HEAD")
	}
	base := commit("base")
	run(root, "clone", "--bare", src, remote)
	run(remote, "config", "http.receivepack", "true")
	head := commit("reviewed")
	run(src, "branch", "export")
	bundle := filepath.Join(root, "review.bundle")
	run(src, "bundle", "create", bundle, "refs/heads/export")
	backend := filepath.Join(run(src, "--exec-path"), "git-http-backend")
	cgiHandler := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=fixture"}}
	var requests, bad atomic.Int64
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("writer:fixture-push-token"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != auth {
			bad.Add(1)
			http.Error(w, "denied", 401)
			return
		}
		cgiHandler.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "http.extraHeader")
	t.Setenv("GIT_CONFIG_VALUE_0", "Authorization: Basic WRONG")
	t.Setenv("GIT_TRACE_CURL", "1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := reviewedPushPlan{cloneURL: server.URL + "/acme/widget.git", branch: "agent/fixture", baseBranch: "main", baseSHA: base, headSHA: head, bundlePath: bundle, username: "writer", password: "fixture-push-token", recordIntent: func(context.Context) error { return nil }}
	verify := func(context.Context) error { return nil }
	reject := func(plan reviewedPushPlan, check func(context.Context) error) {
		t.Helper()
		if err := pushReviewedBundle(ctx, plan, check); !errors.Is(err, ErrReviewedPush) {
			t.Fatalf("expected rejection, got %v", err)
		}
	}
	wrong := req
	wrong.recordIntent = nil
	reject(wrong, verify)
	wrong = req
	wrong.headSHA = base
	reject(wrong, verify)
	wrong = req
	wrong.headSHA = strings.Repeat("a", 40)
	reject(wrong, verify)
	wrong = req
	wrong.branch = "main"
	reject(wrong, verify)
	wrong = req
	wrong.baseSHA = strings.Repeat("b", 40)
	reject(wrong, verify)
	if requests.Load() != 0 {
		t.Fatal("invalid local bundle reached remote")
	}
	reject(req, func(context.Context) error { return errors.New("revoked") })
	if requests.Load() != 0 {
		t.Fatal("revoked authority reached remote")
	}
	// A new remote base, even when the bundle is internally valid, is not
	// the base the operator reviewed.
	run(remote, "update-ref", "refs/heads/main", base)
	stale := req
	stale.baseBranch = "missing"
	reject(stale, verify)
	checks := 0
	reject(req, func(context.Context) error {
		checks++
		if checks == 2 {
			return errors.New("revoked before mutation")
		}
		return nil
	})
	if checks != 2 {
		t.Fatal("authority not rechecked")
	}
	// Another writer wins after preflight. The empty expected old SHA
	// must prevent replacing their branch with the reviewed head.
	race := req
	race.branch = "agent/race"
	checks = 0
	reject(race, func(context.Context) error {
		checks++
		if checks == 2 {
			run(remote, "update-ref", "refs/heads/agent/race", base)
		}
		return nil
	})
	if run(remote, "rev-parse", "refs/heads/agent/race") != base {
		t.Fatal("concurrent branch overwritten")
	}
	if err := pushReviewedBundle(ctx, req, verify); err != nil {
		t.Fatal(err)
	}
	if bad.Load() != 0 || run(remote, "rev-parse", "refs/heads/agent/fixture") != head || run(remote, "rev-parse", "refs/heads/main") != base {
		t.Fatal("unexpected remote state")
	}
	// No overwrite, including a replay of the same approved commit.
	reject(req, verify)
	if run(remote, "rev-parse", "refs/heads/agent/fixture") != head {
		t.Fatal("replay changed remote")
	}
}
