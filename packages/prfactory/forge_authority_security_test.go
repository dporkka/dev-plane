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

	factory := NewFactory(nil, nil).WithGitPushCredential(GitPushCredential{
		Username: "agent",
		Password: "gitea-secret",
	})
	target := repositoryTarget{
		Owner: "acme",
		Name:  "widget",
		Forge: models.RepositoryForgeSettings{
			Provider: models.ForgeProviderGitea,
			BaseURL:  "https://trusted.example",
		},
	}

	err := factory.validateWorkspaceOrigin(context.Background(), repo, target)
	if err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("error = %v, want origin authority mismatch", err)
	}
}

func TestValidateWorkspaceOriginAcceptsCanonicalGiteaHTTPSRemote(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "remote", "add", "origin", "https://trusted.example/acme/widget.git")

	factory := NewFactory(nil, nil).WithGitPushCredential(GitPushCredential{
		Username: "agent",
		Password: "gitea-secret",
	})
	target := repositoryTarget{
		Owner: "acme",
		Name:  "widget",
		Forge: models.RepositoryForgeSettings{
			Provider: models.ForgeProviderGitea,
			BaseURL:  "https://trusted.example",
		},
	}

	if err := factory.validateWorkspaceOrigin(context.Background(), repo, target); err != nil {
		t.Fatalf("validate workspace origin: %v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", filepath.Clean(dir)}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
	}
}
