package prfactory

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrReviewedPush = errors.New("reviewed Gitea push not authorized or not confirmed")

const maxReviewedBundle = 64 << 20

// ReviewedPushRequest is operator-supplied, not an agent command or a webhook.
// The operator must independently review and approve the exact base/head pair.
type ReviewedPushRequest struct {
	GiteaBindingRequest
	TaskID, RunID, ReviewID, ApprovalID              string
	Branch, BaseBranch, HeadSHA, BaseSHA, BundlePath string
	ExpectedGeneration                               int64
}

type reviewedPushPlan struct {
	cloneURL, branch, baseBranch, headSHA, baseSHA, bundlePath string
	username, password                                         string
	recordIntent                                               func(context.Context) error
}

// PublishReviewedGiteaBundle is an opt-in broker boundary. It creates a new
// branch only, never opens a PR or updates a protected/default branch.
func PublishReviewedGiteaBundle(ctx context.Context, db *sql.DB, req ReviewedPushRequest) error {
	if db == nil || req.RepositoryID == "" || req.ProjectID == "" || req.OrganizationID == "" ||
		req.UpstreamRepositoryID <= 0 || req.ExpectedGeneration <= 0 || req.TaskID == "" || req.RunID == "" || req.ReviewID == "" || req.ApprovalID == "" {
		return ErrReviewedPush
	}
	forge, credential, err := ConfiguredGiteaBindingForge()
	if err != nil {
		return ErrReviewedPush
	}
	generation, err := ConfiguredGiteaBindingGeneration()
	if err != nil || generation != req.ExpectedGeneration {
		return ErrReviewedPush
	}
	// All writable credentials are pinned independently of the API credential.
	username, password := os.Getenv("GITEA_GIT_USERNAME"), os.Getenv("GITEA_GIT_PASSWORD")
	pinID, err := strconv.ParseInt(os.Getenv("GITEA_PUSH_REPOSITORY_ID"), 10, 64)
	if err != nil || pinID != req.UpstreamRepositoryID || !validBindingToken(username) || strings.Contains(username, ":") ||
		!validBindingToken(password) || password == credential.AccessToken || password == os.Getenv("GITEA_CLONE_TOKEN") {
		return ErrReviewedPush
	}
	cloneURL := os.Getenv("GITEA_PUSH_URL")
	// Recheck the same durable task/review/approval and repository identity
	// before remote discovery and again immediately before the mutation.
	verify := func(checkCtx context.Context) error {
		if err := verifyReviewedPushApproval(checkCtx, db, req); err != nil {
			return err
		}
		binding, err := VerifyGiteaRepositoryBinding(checkCtx, db, forge, credential, req.RepositoryID, req.ProjectID)
		if err != nil || binding.OrganizationID != req.OrganizationID || binding.UpstreamRepositoryID != req.UpstreamRepositoryID ||
			binding.AuthorizationGeneration != req.ExpectedGeneration || binding.CloneURL != cloneURL {
			return ErrReviewedPush
		}
		return verifyReviewedPushApproval(checkCtx, db, req)
	}
	intent := func(checkCtx context.Context) error {
		return recordReviewedPushIntent(checkCtx, db, req, forge.Authority().BaseURL, cloneURL)
	}
	if err := pushReviewedBundle(ctx, reviewedPushPlan{cloneURL: cloneURL, branch: req.Branch, baseBranch: req.BaseBranch,
		headSHA: req.HeadSHA, baseSHA: req.BaseSHA, bundlePath: req.BundlePath, username: username, password: password,
		recordIntent: intent}, verify); err != nil {
		return err
	}
	return recordReviewedPushReceipt(ctx, db, req)
}

func verifyReviewedPushApproval(ctx context.Context, db *sql.DB, req ReviewedPushRequest) error {
	var found int
	// An approval of some other run, stale workspace or foreign tenant is not
	// authority. The existing review schema has no commit hash: the explicit
	// operator-supplied SHA remains an independent mandatory approval.
	err := db.QueryRowContext(ctx, `
 SELECT 1 FROM tasks t
 JOIN projects p ON p.id=t.project_id AND p.deleted_at IS NULL
 JOIN organizations o ON o.id=p.organization_id AND o.deleted_at IS NULL
 JOIN repositories r ON r.id=t.repository_id AND r.project_id=t.project_id AND r.deleted_at IS NULL
 JOIN agent_runs ar ON ar.task_id=t.id AND ar.id=$1 AND ar.status IN ('completed','reviewed')
 JOIN workspaces w ON w.id=ar.workspace_id AND w.id=t.workspace_id AND w.task_id=t.id
  AND w.repository_id=r.id AND w.deleted_at IS NULL AND w.status='ready' AND w.runtime_provider='docker'
 JOIN review_reports rr ON rr.run_id=ar.id AND rr.id=$2 AND rr.approvable=TRUE
 JOIN approvals a ON a.task_id=t.id AND a.agent_run_id=ar.id AND a.id=$3
  AND a.approval_type='pr_create' AND a.response='approved' AND a.responded_at IS NOT NULL
 JOIN users u ON u.id=a.responded_by AND u.organization_id=o.id AND u.deleted_at IS NULL
 WHERE t.id=$4 AND t.repository_id=$5 AND t.project_id=$6 AND o.id=$7
  AND t.deleted_at IS NULL AND t.status='reviewing'
  AND w.branch=$8 AND w.base_branch=$9 AND t.target_branch=$9 AND r.default_branch<>w.branch
  AND ar.completed_at IS NOT NULL AND ar.completed_at<=rr.created_at AND rr.created_at<=a.responded_at
  AND (a.expires_at IS NULL OR a.responded_at<=a.expires_at)
 `, req.RunID, req.ReviewID, req.ApprovalID, req.TaskID, req.RepositoryID, req.ProjectID, req.OrganizationID, req.Branch, req.BaseBranch).Scan(&found)
	if err != nil || found != 1 {
		return ErrReviewedPush
	}
	return nil
}

func fullSHA(s string) bool {
	if len(s) != 40 || s == strings.Repeat("0", 40) {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validReviewedPush(p reviewedPushPlan) bool {
	if !fullSHA(p.headSHA) || !fullSHA(p.baseSHA) || p.headSHA == p.baseSHA ||
		!strings.HasPrefix(p.branch, "agent/") || p.branch == "agent/" || p.branch == p.baseBranch ||
		p.baseBranch == "" || strings.HasPrefix(p.baseBranch, "-") || strings.ContainsAny(p.branch+p.baseBranch, "\r\n\x00") ||
		!validBindingToken(p.username) || strings.Contains(p.username, ":") || !validBindingToken(p.password) {
		return false
	}
	u, err := url.Parse(p.cloneURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" ||
		!strings.HasSuffix(u.Path, ".git") || strings.ContainsAny(p.cloneURL, "\r\n\x00") {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

// Fresh home and config for each invocation; never load agent Git config,
// hooks, alternates, templates, replace refs, proxy settings or credentials.
func reviewedGitEnv(home, cloneURL, authorization string) []string {
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + home, "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_NO_REPLACE_OBJECTS=1",
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false"}
	config := [][2]string{{"credential.helper", ""}, {"core.hooksPath", "/dev/null"}, {"http.followRedirects", "false"},
		{"http.proxy", ""}, {"http.sslVerify", "true"}, {"protocol.allow", "never"}, {"fetch.fsckObjects", "true"},
		{"transfer.fsckObjects", "true"}, {"gc.auto", "0"}}
	if authorization == "" {
		config = append(config, [2]string{"protocol.file.allow", "always"})
	} else {
		config = append(config, [2]string{"protocol.https.allow", "always"}, [2]string{"protocol.http.allow", "always"},
			[2]string{"http." + cloneURL + ".extraHeader", authorization})
	}
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(config)))
	for i, pair := range config {
		n := strconv.Itoa(i)
		env = append(env, "GIT_CONFIG_KEY_"+n+"="+pair[0], "GIT_CONFIG_VALUE_"+n+"="+pair[1])
	}
	return env
}

type reviewedGitOutput struct{ bytes.Buffer }

func (b *reviewedGitOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64<<10 {
		return 0, ErrReviewedPush
	}
	return b.Buffer.Write(p)
}

func runReviewedGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/git", args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stderr = io.Discard
	out := new(reviewedGitOutput)
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return "", ErrReviewedPush
	}
	return strings.TrimSpace(out.String()), nil
}

func copyReviewedBundle(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxReviewedBundle {
		return ErrReviewedPush
	}
	in, err := os.Open(src)
	if err != nil {
		return ErrReviewedPush
	}
	defer func() { _ = in.Close() }()
	actual, err := in.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		return ErrReviewedPush
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrReviewedPush
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, maxReviewedBundle+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || n != info.Size() || n > maxReviewedBundle {
		return ErrReviewedPush
	}
	return nil
}

// Only a full bundle with the explicit refs/heads/export ref is accepted.
// The credential-bearing operation runs in a new bare repository; no agent
// worktree, agent-origin remote, checkout, hook or arbitrary refspec is used.
func pushReviewedBundle(ctx context.Context, p reviewedPushPlan, verify func(context.Context) error) error {
	if !validReviewedPush(p) || verify == nil || p.recordIntent == nil {
		return ErrReviewedPush
	}
	root, err := os.MkdirTemp("", "devplane-reviewed-push-")
	if err != nil {
		return ErrReviewedPush
	}
	defer func() { _ = os.RemoveAll(root) }()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(home, 0700); err != nil {
		return ErrReviewedPush
	}
	bundle := filepath.Join(root, "review.bundle")
	if err := copyReviewedBundle(p.bundlePath, bundle); err != nil {
		return err
	}
	local := reviewedGitEnv(home, "", "")
	git := func(dir string, args ...string) (string, error) { return runReviewedGit(ctx, dir, local, args...) }
	dest, base := "refs/heads/"+p.branch, "refs/heads/"+p.baseBranch
	for _, ref := range []string{dest, base} {
		if _, err := git(root, "check-ref-format", ref); err != nil {
			return err
		}
	}
	if _, err := git(root, "init", "--bare", "--template=", "--object-format=sha1", repo); err != nil {
		return err
	}
	if _, err := git(repo, "bundle", "verify", bundle); err != nil {
		return err
	}
	if _, err := git(repo, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--", bundle, "refs/heads/export:refs/heads/reviewed"); err != nil {
		return err
	}
	head, err := git(repo, "rev-parse", "--verify", "refs/heads/reviewed^{commit}")
	if err != nil || head != p.headSHA {
		return ErrReviewedPush
	}
	baseType, err := git(repo, "cat-file", "-t", p.baseSHA)
	if err != nil || baseType != "commit" {
		return ErrReviewedPush
	}
	if _, err := git(repo, "merge-base", "--is-ancestor", p.baseSHA, p.headSHA); err != nil {
		return err
	}
	if _, err := git(repo, "fsck", "--strict", "--full", "--no-reflogs"); err != nil {
		return err
	}
	if err := verify(ctx); err != nil {
		return ErrReviewedPush
	}
	auth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(p.username+":"+p.password))
	remote := reviewedGitEnv(home, p.cloneURL, auth)
	ls := func(ref string) (string, error) {
		return runReviewedGit(ctx, repo, remote, "ls-remote", "--refs", "--", p.cloneURL, ref)
	}
	got, err := ls(base)
	if err != nil || got != p.baseSHA+"\t"+base {
		return ErrReviewedPush
	}
	got, err = ls(dest)
	if err != nil || got != "" {
		return ErrReviewedPush
	}
	if err := verify(ctx); err != nil {
		return ErrReviewedPush
	}
	// Consume the approval before attempting the external mutation. A crash
	// between this write and the receipt leaves an intent for reconciliation.
	if err := p.recordIntent(ctx); err != nil {
		return err
	}
	// An empty expected old SHA means create-only. Even if another writer
	// races the preflight, Git must not replace an existing branch.
	if _, err := runReviewedGit(ctx, repo, remote, "push", "--porcelain", "--no-verify", "--force-with-lease="+dest+":", "--", p.cloneURL, p.headSHA+":"+dest); err != nil {
		return fmt.Errorf("%w: push failed or outcome uncertain; inspect upstream before retry", ErrReviewedPush)
	}
	got, err = ls(dest)
	if err != nil || got != p.headSHA+"\t"+dest {
		return fmt.Errorf("%w: pushed head not independently confirmed", ErrReviewedPush)
	}
	return nil
}
