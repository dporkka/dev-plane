package runtimes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareNulangLocalCheckoutPinsExactHeadAndStripsRemoteCredentials(t *testing.T) {
	source := initLocalSourceRepo(t)
	head := gitOutput(t, source, "rev-parse", "HEAD")

	repoDir, preparedHead, cleanup, err := prepareNulangLocalCheckout(
		context.Background(),
		source,
		head,
		"https://ci-user:super-secret@example.com/org/repo.git",
	)
	if err != nil {
		t.Fatalf("prepareNulangLocalCheckout() error = %v", err)
	}
	defer cleanup()

	if preparedHead != head {
		t.Fatalf("prepared head = %q, want %q", preparedHead, head)
	}
	if got := gitOutput(t, repoDir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("cloned HEAD = %q, want %q", got, head)
	}
	if got := gitOutput(t, repoDir, "remote", "get-url", "origin"); got != "https://example.com/org/repo.git" {
		t.Fatalf("sanitized origin = %q", got)
	}
	configBytes, err := os.ReadFile(filepath.Join(repoDir, ".git", "config"))
	if err != nil {
		t.Fatalf("read cloned git config: %v", err)
	}
	if strings.Contains(string(configBytes), "super-secret") || strings.Contains(string(configBytes), "ci-user") {
		t.Fatalf("prepared checkout retained credentials: %s", configBytes)
	}
}

func TestPrepareNulangLocalCheckoutRejectsSourceHeadDrift(t *testing.T) {
	source := initLocalSourceRepo(t)
	wrongHead := strings.Repeat("a", 40)

	_, _, cleanup, err := prepareNulangLocalCheckout(
		context.Background(),
		source,
		wrongHead,
		"https://example.com/org/repo.git",
	)
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "source HEAD mismatch") {
		t.Fatalf("prepareNulangLocalCheckout() error = %v, want source HEAD mismatch", err)
	}
}

func TestPrepareNulangLocalCheckoutRemovesHostLocalOriginWithoutProvenanceURL(t *testing.T) {
	source := initLocalSourceRepo(t)
	head := gitOutput(t, source, "rev-parse", "HEAD")

	repoDir, _, cleanup, err := prepareNulangLocalCheckout(context.Background(), source, head, "")
	if err != nil {
		t.Fatalf("prepareNulangLocalCheckout() error = %v", err)
	}
	defer cleanup()

	cmd := exec.Command("git", "-C", repoDir, "remote")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git remote: %v", err)
	}
	if strings.TrimSpace(string(output)) != "" {
		t.Fatalf("prepared checkout retained local origin: %q", output)
	}
}

func initLocalSourceRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "ci@example.invalid")
	runGit(t, dir, "config", "user.name", "CI")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("verified source\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}
