package prfactory

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/models"
)

func TestWorkspaceOriginRejectsCredentialRedirectBeforePush(t *testing.T) {
	repo := t.TempDir()
	runOriginGit(t, repo, "init")
	runOriginGit(t, repo, "remote", "add", "origin", "https://attacker.example/acme/widget.git")

	err := originCredentialFactory().validateWorkspaceOrigin(
		context.Background(), repo, originGiteaTarget("https://trusted.example"),
	)
	if err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("error = %v, want origin authority mismatch", err)
	}
}

func TestWorkspaceOriginRejectsDifferentPort(t *testing.T) {
	repo := t.TempDir()
	runOriginGit(t, repo, "init")
	runOriginGit(t, repo, "remote", "add", "origin", "https://trusted.example/acme/widget.git")

	err := originCredentialFactory().validateWorkspaceOrigin(
		context.Background(), repo, originGiteaTarget("https://trusted.example:8443"),
	)
	if err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("error = %v, want port/authority mismatch", err)
	}
}

func TestWorkspaceOriginRejectsHTTPSDowngrade(t *testing.T) {
	repo := t.TempDir()
	runOriginGit(t, repo, "init")
	runOriginGit(t, repo, "remote", "add", "origin", "http://trusted.example/acme/widget.git")

	err := originCredentialFactory().validateWorkspaceOrigin(
		context.Background(), repo, originGiteaTarget("https://trusted.example"),
	)
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error = %v, want scheme downgrade rejection", err)
	}
}

func TestWorkspaceOriginAcceptsCanonicalGiteaHTTPSRemote(t *testing.T) {
	repo := t.TempDir()
	runOriginGit(t, repo, "init")
	runOriginGit(t, repo, "remote", "add", "origin", "https://trusted.example/acme/widget.git")

	if err := originCredentialFactory().validateWorkspaceOrigin(
		context.Background(), repo, originGiteaTarget("https://trusted.example"),
	); err != nil {
		t.Fatalf("validate workspace origin: %v", err)
	}
}

func TestWorkspaceOriginAcceptsCanonicalSCPLikeSSHRemoteWithoutHTTPSCredential(t *testing.T) {
	repo := t.TempDir()
	runOriginGit(t, repo, "init")
	runOriginGit(t, repo, "remote", "add", "origin", "git@trusted.example:acme/widget.git")

	factory := NewFactory(nil, nil)
	if err := factory.validateWorkspaceOrigin(
		context.Background(), repo, originGiteaTarget("https://trusted.example"),
	); err != nil {
		t.Fatalf("validate scp-style workspace origin: %v", err)
	}
}

func originCredentialFactory() *Factory {
	return NewFactory(nil, nil).WithGitPushCredential(GitPushCredential{
		Username: "agent",
		Password: "gitea-secret",
	})
}

func originGiteaTarget(baseURL string) repositoryTarget {
	return repositoryTarget{
		Owner: "acme",
		Name:  "widget",
		Forge: models.RepositoryForgeSettings{
			Provider: models.ForgeProviderGitea,
			BaseURL:  baseURL,
		},
	}
}

func runOriginGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", filepath.Clean(dir)}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
	}
}
