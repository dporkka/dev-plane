package runtimes

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerProviderIntegrationIsolationAndCleanup(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("set RUN_DOCKER_INTEGRATION=1 to run live Docker runtime tests")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI not available: %v", err)
	}
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git CLI not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	repoDir := createIntegrationRepo(t)
	hostSecret := filepath.Join(t.TempDir(), "host-secret.txt")
	if err := os.WriteFile(hostSecret, []byte("do not mount"), 0600); err != nil {
		t.Fatalf("write host secret: %v", err)
	}

	provider, err := NewDockerProvider(t.TempDir())
	if err != nil {
		t.Fatalf("NewDockerProvider() error: %v", err)
	}
	session, err := provider.CreateWorkspace(ctx, CreateRequest{
		RepositoryID: "repo-integration",
		CloneURL:     repoDir,
		BaseBranch:   "main",
		Branch:       "agent/integration",
		WorktreeName: "workspace-integration",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace() error: %v", err)
	}
	defer func() {
		_ = provider.DestroyWorkspace(context.Background(), session.ID)
	}()

	containerName := "dev-plane-" + session.ID
	volumeName := "dev-plane-" + session.ID + "-workspace"
	assertDockerHostConfig(t, containerName)

	data, err := provider.ReadFile(ctx, session.ID, "README.md")
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if strings.TrimSpace(string(data)) != "# Integration" {
		t.Fatalf("ReadFile() = %q, want integration README", string(data))
	}
	if err := provider.WriteFile(ctx, session.ID, "generated.txt", []byte("inside container\n")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	written, err := provider.ReadFile(ctx, session.ID, "generated.txt")
	if err != nil {
		t.Fatalf("ReadFile(generated) error: %v", err)
	}
	if string(written) != "inside container\n" {
		t.Fatalf("generated content = %q", string(written))
	}

	hostPathCheck := "test ! -e " + shellPath(hostSecret)
	result, err := provider.ExecuteCommand(ctx, session.ID, Command{Command: hostPathCheck, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("host path isolation command error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("host path isolation exit = %d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}

	networkCheck := "git ls-remote https://github.com/github/gitignore >/tmp/network-check.out 2>&1; test $? -ne 0"
	result, err = provider.ExecuteCommand(ctx, session.ID, Command{Command: networkCheck, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatalf("network isolation command error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("network isolation exit = %d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}

	if err := provider.DestroyWorkspace(ctx, session.ID); err != nil {
		t.Fatalf("DestroyWorkspace() error: %v", err)
	}
	if _, err := provider.GetStatus(ctx, session.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetStatus() after destroy error = %v, want ErrSessionNotFound", err)
	}
	if exec.Command("docker", "inspect", containerName).Run() == nil {
		t.Fatalf("container %s still exists after destroy", containerName)
	}
	if exec.Command("docker", "volume", "inspect", volumeName).Run() == nil {
		t.Fatalf("volume %s still exists after destroy", volumeName)
	}
}

func createIntegrationRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "test@example.invalid")
	runGit(t, repoDir, "config", "user.name", "Runtime Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Integration\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-m", "initial")
	return repoDir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(output))
	}
}

func assertDockerHostConfig(t *testing.T, containerName string) {
	t.Helper()
	format := "{{.HostConfig.NetworkMode}}|{{.HostConfig.ReadonlyRootfs}}|{{.HostConfig.PidsLimit}}|{{.HostConfig.Memory}}|{{.HostConfig.NanoCpus}}|{{range .HostConfig.CapDrop}}{{.}},{{end}}|{{range .HostConfig.SecurityOpt}}{{.}},{{end}}"
	output, err := exec.Command("docker", "inspect", "-f", format, containerName).CombinedOutput()
	if err != nil {
		t.Fatalf("docker inspect host config: %v\n%s", err, string(output))
	}
	parts := strings.Split(strings.TrimSpace(string(output)), "|")
	if len(parts) != 7 {
		t.Fatalf("unexpected docker inspect output: %q", string(output))
	}
	if parts[0] != "none" {
		t.Fatalf("network mode = %q, want none", parts[0])
	}
	if parts[1] != "true" {
		t.Fatalf("readonly rootfs = %q, want true", parts[1])
	}
	if parts[2] == "0" || parts[2] == "-1" {
		t.Fatalf("pids limit = %q, want constrained", parts[2])
	}
	if parts[3] == "0" {
		t.Fatalf("memory limit = %q, want constrained", parts[3])
	}
	if parts[4] == "0" {
		t.Fatalf("cpu limit = %q, want constrained", parts[4])
	}
	if !strings.Contains(parts[5], "ALL") {
		t.Fatalf("cap drop = %q, want ALL", parts[5])
	}
	if !strings.Contains(parts[6], "no-new-privileges") {
		t.Fatalf("security opts = %q, want no-new-privileges", parts[6])
	}
}

func shellPath(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}
