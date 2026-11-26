package prfactory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

func TestRuntimeFactorySelectsGiteaAndSeparatesCredentials(t *testing.T) {
	t.Setenv("FORGE_PROVIDER", "gitea")
	t.Setenv("GITHUB_TOKEN", "github-must-not-be-used")
	t.Setenv("GITEA_TOKEN", "synthetic-api-token/")
	t.Setenv("GITEA_GIT_USERNAME", "agent")
	t.Setenv("GITEA_GIT_PASSWORD", "synthetic-push-password/")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/git/api/v1/repos/acme/widget/pulls" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "token synthetic-api-token/" {
			t.Error("wrong forge API credential")
		}
		var req struct {
			Head  string
			Base  string
			Title string
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Head != "feature" || req.Base != "main" {
			t.Error("unexpected PR creation request")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":12,"number":7,"title":"agent","state":"open","head":{"ref":"feature","sha":"abc"},"base":{"ref":"main"}}`))
	}))
	defer srv.Close()
	t.Setenv("GITEA_URL", srv.URL+"/git/")
	f := NewFactory(nil, nil)
	auth, ok := f.forge.(gateway.ForgeAuthorityProvider)
	if !ok || auth.Authority().Provider != "gitea" || auth.Authority().BaseURL != srv.URL+"/git" {
		t.Fatal("factory did not select the configured Gitea instance")
	}
	if f.forgeCredential.AccessToken != "synthetic-api-token/" || f.gitPushCredential != (GitPushCredential{Username: "agent", Password: "synthetic-push-password/"}) || f.githubToken != "" {
		t.Fatal("forge API and Git push credentials are not isolated")
	}
	if err := f.validateRepositoryForge(repositoryTarget{Owner: "acme", Name: "widget", Forge: models.RepositoryForgeSettings{Provider: models.ForgeProviderGitea, BaseURL: srv.URL + "/git"}}); err != nil {
		t.Fatalf("matching authority rejected: %v", err)
	}
	pr, err := f.createForgePR(context.Background(), "acme", "widget", "agent", "body", "feature", "main", false)
	if err != nil || pr.Number != 7 || calls != 1 {
		t.Fatalf("unexpected PR result: %v; calls=%d", err, calls)
	}
}

func TestRuntimeFactoryGiteaSSHDoesNotInheritGitHubCredentials(t *testing.T) {
	t.Setenv("FORGE_PROVIDER", "gitea")
	t.Setenv("GITHUB_TOKEN", "github-must-not-be-used")
	t.Setenv("GITEA_URL", "https://forge.example")
	t.Setenv("GITEA_TOKEN", "synthetic-api-token/")
	t.Setenv("GITEA_GIT_USERNAME", "")
	t.Setenv("GITEA_GIT_PASSWORD", "")
	f := NewFactory(nil, nil)
	if f.forge == nil || f.forge.Name() != "gitea" || f.gitPushCredential != (GitPushCredential{}) {
		t.Fatal("Gitea SSH mode inherited unrelated HTTPS credentials")
	}
}
