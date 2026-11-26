package prfactory

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/models"
)

func TestRepositoryTargetWorkspaceOriginSubpathAuthority(t *testing.T) {
	target := repositoryTarget{
		Owner: "acme",
		Name:  "widget",
		Forge: models.RepositoryForgeSettings{
			Provider: models.ForgeProviderGitea,
			BaseURL:  "https://trusted.example/git",
		},
	}
	factory := NewFactory(nil, nil).WithGitPushCredential(GitPushCredential{
		Username: "agent",
		Password: "synthetic-test-credential",
	})
	for _, tc := range []struct {
		name, remote, wantError string
	}{
		{name: "canonical subpath", remote: "https://trusted.example/git/acme/widget.git"},
		{name: "missing instance subpath", remote: "https://trusted.example/acme/widget.git", wantError: "repository mismatch"},
		{name: "different repository", remote: "https://trusted.example/git/acme/other.git", wantError: "repository mismatch"},
		{name: "embedded HTTP credential", remote: "https://agent:synthetic@trusted.example/git/acme/widget.git", wantError: "embedded HTTP credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			for _, args := range [][]string{{"init", "--quiet"}, {"remote", "add", "origin", tc.remote}} {
				cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", repo}, args...)...)
				if err := cmd.Run(); err != nil {
					t.Fatalf("initialize synthetic repository: %v", err)
				}
			}
			err := factory.validateWorkspaceOrigin(context.Background(), repo, target)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("validate canonical origin: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected %q rejection, got %v", tc.wantError, err)
			}
		})
	}
}
