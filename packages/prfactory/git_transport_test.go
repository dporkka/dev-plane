package prfactory

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyGitPushRemoteRejectsMixedCaseOrigin(t *testing.T) {
	for _, origin := range []string{
		"https://github.com/Team/repo.git",
		"https://github.com/team/Repo",
	} {
		t.Run(origin, func(t *testing.T) {
			path := gitRepoForPushTest(t)
			addPushRemote(t, path, origin)
			err := verifyGitPushRemote(context.Background(), path, "agent/feature", "team", "repo", sanitizedGitEnvironment())
			if err == nil || !strings.Contains(err.Error(), "does not match authorized") {
				t.Fatalf("mixed case origin should be rejected: %v", err)
			}
		})
	}
}

func TestGitTransportRejectsMaliciousRepositorySegments(t *testing.T) {
	for _, v := range [][2]string{{"team/other", "repo"}, {"team", "../repo"}, {"team", "evil:repo"}, {"", "repo"}, {"team", ""}, {"team", "repo?x"}} {
		if _, err := trustedGitHubPushURL(v[0], v[1]); err == nil {
			t.Errorf("accepted unsafe segments %q/%q", v[0], v[1])
		}
	}
}

func TestSanitizedGitEnvironmentDropsAmbientProxyAndTrustOverrides(t *testing.T) {
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "CURL_CA_BUNDLE", "SSL_CERT_FILE", "SSL_CERT_DIR", "GIT_SSL_NO_VERIFY"} {
		t.Setenv(key, "http://attacker.invalid")
	}
	for _, item := range sanitizedGitEnvironment() {
		key := strings.SplitN(item, "=", 2)[0]
		for _, banned := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "CURL_CA_BUNDLE", "SSL_CERT_FILE", "SSL_CERT_DIR", "GIT_SSL_NO_VERIFY"} {
			if strings.EqualFold(key, banned) {
				t.Errorf("inherited unsafe environment %q", key)
			}
		}
	}
	if os.Getenv("HTTPS_PROXY") != "http://attacker.invalid" {
		t.Fatal("test modified parent environment")
	}
}

func TestPushBranchIsolatesTransportFromWorkspaceConfig(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Env = sanitizedGitEnvironment()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	repo := gitRepoForPushTest(t)
	addPushRemote(t, repo, "https://github.com/team/repo")
	runGit("-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com",
		"commit", "--allow-empty", "-qm", "source commit")
	worktree := filepath.Join(t.TempDir(), "worktree")
	runGit("-C", repo, "worktree", "add", "-b", "agent/feature", worktree)
	for _, pair := range [][2]string{
		{"remote.origin.proxy", "http://proxy.invalid:8080"},
		{"http.https://github.com/team/repo.git.proxy", "http://proxy.invalid:8080"},
		{"http.https://github.com/team/repo.git.sslVerify", "false"},
		{"http.https://github.com/team/repo.git.sslCAInfo", "/bad/ca.pem"},
	} {
		runGit("-C", worktree, "config", pair[0], pair[1])
	}
	commit := runGit("-C", worktree, "rev-parse", "HEAD")
	target := t.TempDir()
	runGit("init", "-q", "--bare", target)

	// Check the actual credentialed invocation, then forward its refspec to
	// a local bare repository to exercise packing and updating the branch.
	bin := t.TempDir()
	source := `#!/bin/sh
set -eu
[ "$1" = "-C" ] || exec "$TEST_REAL_GIT" "$@"
dir="$2"
shift 2
tls_verify=
while [ "${1:-}" = "-c" ]; do
  case "$2" in
    http.sslVerify=true) tls_verify=yes ;;
    http.*sslCAInfo=*|http.*sslCAPath=*) exit 1 ;;
  esac
  shift 2
done
if [ "${1:-}" != "push" ]; then
  exec "$TEST_REAL_GIT" -C "$dir" "$@"
fi
[ "$2" = "--" ]
[ "$3" = "https://github.com/team/repo.git" ]
[ "$4" = "$TEST_COMMIT:refs/heads/agent/feature" ]
[ "$tls_verify" = "yes" ]
[ -z "${HTTPS_PROXY+x}" ]
[ -z "${CURL_CA_BUNDLE+x}" ]
[ "$("$GIT_ASKPASS" 'Username')" = "x-access-token" ]
[ "$("$GIT_ASKPASS" 'Password')" = "dummy" ]
if "$TEST_REAL_GIT" -C "$dir" config --local --get-regexp '^(http\.|remote\.)'; then
  exit 1
fi
GIT_ALLOW_PROTOCOL=file exec "$TEST_REAL_GIT" -C "$dir" push -- "$TEST_TARGET" "$4"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_REAL_GIT", git)
	t.Setenv("TEST_COMMIT", commit)
	t.Setenv("TEST_TARGET", target)
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:9000")
	t.Setenv("CURL_CA_BUNDLE", "/bad/ambient-ca.pem")

	if err := (&Factory{}).pushBranch(context.Background(), worktree, "agent/feature", "team", "repo", "dummy"); err != nil {
		t.Fatal(err)
	}
	if got := runGit("-C", target, "rev-parse", "refs/heads/agent/feature"); got != commit {
		t.Fatalf("pushed commit %s, want %s", got, commit)
	}
}
