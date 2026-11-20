package prfactory

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizedGitEnvironmentDropsInheritedRepositorySelectors(t *testing.T) {
	for _, key := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_NAMESPACE", "GIT_EXEC_PATH", "GIT_TEMPLATE_DIR",
		"GIT_SSH_COMMAND", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0",
		"GIT_CONFIG_VALUE_0",
	} {
		t.Setenv(key, "/tmp/untrusted-git")
	}
	got := sanitizedGitEnvironment()
	for _, v := range got {
		if !strings.HasPrefix(v, "GIT_") {
			continue
		}
		if strings.HasPrefix(v, "GIT_CONFIG_GLOBAL=/dev/null") ||
			strings.HasPrefix(v, "GIT_CONFIG_NOSYSTEM=1") ||
			strings.HasPrefix(v, "GIT_ALLOW_PROTOCOL=https") ||
			strings.HasPrefix(v, "GIT_TERMINAL_PROMPT=0") {
			continue
		}
		t.Errorf("unexpected inherited Git setting: %s", strings.SplitN(v, "=", 2)[0])
	}
}

func TestPushRemoteCheckCannotBeRedirectedByInheritedGitDir(t *testing.T) {
	create := func(remote string) string {
		t.Helper()
		path := t.TempDir()
		cmd := exec.Command("git", "init", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("init %q: %v %s", path, err, out)
		}
		cmd = exec.Command("git", "-C", path, "remote", "add", "origin", remote)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote: %v %s", err, out)
		}
		return path
	}
	authorized := create("https://github.com/trusted/repo.git")
	attacker := create("https://github.com/attacker/wrong.git")
	t.Setenv("GIT_DIR", filepath.Join(attacker, ".git"))
	t.Setenv("GIT_WORK_TREE", attacker)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(attacker, ".git", "index"))
	if err := verifyGitPushRemote(context.Background(), authorized, "agent/feature", "trusted", "repo", sanitizedGitEnvironment()); err != nil {
		t.Fatalf("selected repo was overridden by inherited GIT_DIR: %v", err)
	}
	// Verify the protected check still rejects an actual wrong remote.
	if err := verifyGitPushRemote(context.Background(), attacker, "agent/feature", "trusted", "repo", sanitizedGitEnvironment()); err == nil {
		t.Fatal("unauthorized repository passed remote check")
	}
}

func TestSanitizedGitEnvironmentKeepsLockedTransportSettings(t *testing.T) {
	t.Setenv("GIT_ALLOW_PROTOCOL", "file:ssh")
	t.Setenv("GIT_CONFIG_GLOBAL", "/tmp/evilconfig")
	got := sanitizedGitEnvironment()
	expected := []string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_ALLOW_PROTOCOL=https", "GIT_TERMINAL_PROMPT=0",
	}
	for _, want := range expected {
		var count int
		for _, entry := range got {
			if entry == want {
				count++
			}
		}
		if count != 1 {
			t.Errorf("want exactly one %s, got %d", want, count)
		}
	}
	if value := os.Getenv("GIT_ALLOW_PROTOCOL"); value != "file:ssh" {
		t.Fatalf("test altered process env: %s", value)
	}
}
