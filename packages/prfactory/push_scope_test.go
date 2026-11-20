package prfactory

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func gitRepoForPushTest(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if out, err := exec.Command("git", "init", path).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	return path
}
func addPushRemote(t *testing.T, path, remote string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", path, "remote", "add", "origin", remote).CombinedOutput(); err != nil {
		t.Fatalf("git remote: %s: %v", out, err)
	}
}
func TestPushBranchRejectsOtherOrganizationBeforeGitAuthentication(t *testing.T) {
	path := gitRepoForPushTest(t)
	addPushRemote(t, path, "https://github.com/other-tenant/repo.git")
	f := NewFactory(nil, nil)
	err := f.pushBranch(context.Background(), path, "agent/patch", "my-tenant", "repo", "secret")
	if err == nil || !strings.Contains(err.Error(), "remote") {
		t.Fatalf("push must refuse cross-tenant remote: %v", err)
	}
}
func TestPushBranchRejectsMaliciousGitURLRewrite(t *testing.T) {
	path := gitRepoForPushTest(t)
	addPushRemote(t, path, "https://github.com/my-tenant/repo.git")
	if out, err := exec.Command("git", "-C", path, "config", "url.https://attacker.invalid/x/.insteadOf", "https://github.com/").CombinedOutput(); err != nil {
		t.Fatalf("config: %s: %v", out, err)
	}
	f := NewFactory(nil, nil)
	err := f.pushBranch(context.Background(), path, "agent/patch", "my-tenant", "repo", "secret")
	if err == nil || !strings.Contains(err.Error(), "remote") {
		t.Fatalf("rewritten git remote must be rejected: %v", err)
	}
}
func TestValidatePushRemoteExactRepository(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://github.com/team/repo.git", true},
		{"https://github.com/team/repo", true},
		{"https://github.com/other/repo.git", false},
		{"https://github.com/team/other.git", false},
		{"https://attacker.invalid/team/repo.git", false},
		{"https://user:secret@github.com/team/repo.git", false},
		{"http://github.com/team/repo.git", false},
		{"https://github.com.evil.invalid/team/repo.git", false},
		{"https://github.com/team/repo.git?token=x", false},
		{"git@github.com:team/repo.git", false},
	}
	for _, tt := range cases {
		t.Run(tt.url, func(t *testing.T) {
			if got := validGitHubPushURL(tt.url, "team", "repo"); got != tt.ok {
				t.Errorf("validGitHubPushURL()=%v want %v", got, tt.ok)
			}
		})
	}
}
