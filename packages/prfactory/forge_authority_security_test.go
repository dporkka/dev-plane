package prfactory

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

func TestValidateRepositoryForgeRejectsDifferentGiteaInstance(t *testing.T) {
	forge, err := gateway.NewGiteaForge("https://trusted.example", nil)
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}
	factory := NewFactory(nil, nil).WithForge(forge, gateway.ForgeCredential{AccessToken: "secret"})
	target := repositoryTarget{
		Owner: "acme",
		Name:  "widget",
		Forge: models.RepositoryForgeSettings{
			Provider: models.ForgeProviderGitea,
			BaseURL:  "https://attacker.example",
		},
	}

	err = factory.validateRepositoryForge(target)
	if err == nil || !strings.Contains(err.Error(), "forge instance") {
		t.Fatalf("error = %v, want forge instance mismatch", err)
	}
}

func TestValidateWorkspaceOriginRejectsCredentialRedirectBeforePush(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "remote", "add", "origin", "https://attacker.example/acme/widget.git")

	factory := credentialedFactory()
	target := giteaTarget("https://trusted.example")

	err := factory.validateWorkspaceOrigin(context.Background(), repo, target)
	if err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("error = %v, want origin authority mismatch", err)
	}
}

func TestValidateWorkspaceOriginRejectsDifferentPort(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "remote", "add", "origin", "https://trusted.example/acme/widget.git")

	err := credentialedFactory().validateWorkspaceOrigin(
		context.Background(), repo, giteaTarget("https://trusted.example:8443"),
	)
	if err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("error = %v, want port/authority mismatch", err)
	}
}

func TestValidateWorkspaceOriginRejectsHTTPSDowngrade(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "remote", "add", "origin", "http://trusted.example/acme/widget.git")

	err := credentialedFactory().validateWorkspaceOrigin(
		context.Background(), repo, giteaTarget("https://trusted.example"),
	)
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error = %v, want scheme downgrade rejection", err)
	}
}

func TestValidateWorkspaceOriginAcceptsCanonicalGiteaHTTPSRemote(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "remote", "add", "origin", "https://trusted.example/acme/widget.git")

	if err := credentialedFactory().validateWorkspaceOrigin(
		context.Background(), repo, giteaTarget("https://trusted.example"),
	); err != nil {
		t.Fatalf("validate workspace origin: %v", err)
	}
}

func credentialedFactory() *Factory {
	return NewFactory(nil, nil).WithGitPushCredential(GitPushCredential{
		Username: "agent",
		Password: "gitea-secret",
	})
}

func giteaTarget(baseURL string) repositoryTarget {
	return repositoryTarget{
		Owner: "acme",
		Name:  "widget",
		Forge: models.RepositoryForgeSettings{
			Provider: models.ForgeProviderGitea,
			BaseURL:  baseURL,
		},
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", filepath.Clean(dir)}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
	}
}
