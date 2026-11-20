package prfactory

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitTransportUsesPinnedHTTPSSettingsAgainstWorkspaceConfig(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q", repo)
	cmd.Env = sanitizedGitEnvironment()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	for _, pair := range [][2]string{
		{"http.https://github.com/team/repo.git.proxy", "http://attacker.invalid:8080"},
		{"http.https://github.com/team/repo.git.sslVerify", "false"},
		{"http.https://github.com/team/repo.git.sslCAInfo", "/tmp/attacker-ca.pem"},
		{"http.https://github.com/team/repo.git.followRedirects", "true"},
		{"http.https://github.com/team/repo.git.extraHeader", "Authorization: attacker"},
		{"remote.origin.proxy", "http://attacker.invalid:9000"},
	} {
		cmd = exec.Command("git", "-C", repo, "config", pair[0], pair[1])
		cmd.Env = sanitizedGitEnvironment()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("config: %v %s", err, out)
		}
	}
	trusted, err := trustedGitTransportArgs("team", "repo")
	if err != nil {
		t.Fatal(err)
	}
	values := []struct{ key, expected string }{
		{"http.proxy", ""},
		{"http.sslVerify", "true"},
		{"http.sslCAInfo", ""},
		{"http.followRedirects", "false"},
		{"http.extraHeader", ""},
	}
	for _, v := range values {
		t.Run(v.key, func(t *testing.T) {
			args := append([]string{"-C", repo}, trusted...)
			args = append(args, "config", "--get-urlmatch", v.key, "https://github.com/team/repo.git")
			cmd := exec.Command("git", args...)
			cmd.Env = sanitizedGitEnvironment()
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != v.expected {
				t.Fatalf("%s: got %q, want %q; err=%v", v.key, out, v.expected, err)
			}
		})
	}
	args := append([]string{"-C", repo}, trusted...)
	args = append(args, "config", "--get", "remote.origin.proxy")
	cmd = exec.Command("git", args...)
	cmd.Env = sanitizedGitEnvironment()
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("remote proxy override=%q err=%v", out, err)
	}
}
func TestGitTransportNoDotGitURLAlsoPinned(t *testing.T) {
	args, err := trustedGitTransportArgs("team", "repo")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"http.https://github.com/team/repo.proxy=", "http.https://github.com/team/repo.sslVerify=true"} {
		var found bool
		for _, s := range args {
			if s == key {
				found = true
			}
		}
		if !found {
			t.Errorf("unprotected no-dot-git transport setting %q", key)
		}
	}
}
func TestGitTransportRejectsMaliciousRepositorySegments(t *testing.T) {
	for _, v := range [][2]string{{"team/other", "repo"}, {"team", "../repo"}, {"team", "evil:repo"}, {"", "repo"}, {"team", ""}, {"team", "repo?x"}} {
		if _, err := trustedGitTransportArgs(v[0], v[1]); err == nil {
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

func TestTrustedPushCommandAppliesTransportBeforeRemoteInvocation(t *testing.T) {
	args, err := trustedGitPushCommandArgs("/tmp/worktree", "feature/security", "team", "repo")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, v := range []string{
		"-C /tmp/worktree", "-c http.https://github.com/team/repo.git.proxy=",
		"-c http.https://github.com/team/repo.git.sslVerify=true",
		"-c http.https://github.com/team/repo.git.followRedirects=false",
		"-c core.hooksPath=/dev/null", "-c credential.helper=",
		"push -- origin feature/security",
	} {
		if !strings.Contains(joined, v) {
			t.Errorf("command lost trusted option %q: %s", v, joined)
		}
	}
	if _, err := trustedGitPushCommandArgs("/tmp/worktree", "feature/x", "team/other", "repo"); err == nil {
		t.Fatal("accepted invalid GitHub organization")
	}
}

func TestRealPushBranchUsesPinnedTransportWithoutNetwork(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	source := `#!/bin/sh
for arg do
  case "$arg" in
    check-ref-format) exit 0 ;;
    remote) printf '%s\n' 'https://github.com/team/repo.git'; exit 0 ;;
    push)
      printf '%s\n' "$@" > "$TEST_CAPTURE_ARGS"
      env > "$TEST_CAPTURE_ENV"
      exit 0
      ;;
  esac
done
exit 7
`
	if err := os.WriteFile(script, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TEST_CAPTURE_ARGS", filepath.Join(bin, "args.txt"))
	t.Setenv("TEST_CAPTURE_ENV", filepath.Join(bin, "env.txt"))
	t.Setenv("HTTPS_PROXY", "http://attacker.invalid:9000")
	t.Setenv("CURL_CA_BUNDLE", "/tmp/attacker-root.pem")
	f := &Factory{}
	if err := f.pushBranch(context.Background(), t.TempDir(), "agent/feature", "team", "repo", "dummy"); err != nil {
		t.Fatal(err)
	}
	argsBytes, err := os.ReadFile(filepath.Join(bin, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsBytes)
	for _, want := range []string{"http.https://github.com/team/repo.git.proxy=",
		"http.https://github.com/team/repo.git.sslVerify=true",
		"http.https://github.com/team/repo.git.sslCAInfo=",
		"http.https://github.com/team/repo.git.followRedirects=false",
		"credential.helper=", "push\n", "origin\n", "agent/feature\n"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in actual git push args:\n%s", want, args)
		}
	}
	envBytes, err := os.ReadFile(filepath.Join(bin, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(envBytes)
	if strings.Contains(env, "attacker.invalid") || strings.Contains(env, "attacker-root.pem") {
		t.Errorf("ambient proxy/CA reached push process")
	}
}
