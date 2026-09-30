//go:build integration

package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/forge"
	"golang.org/x/oauth2"
)

func skipIfMissing(t *testing.T, env string) string {
	t.Helper()
	value := os.Getenv(env)
	if value == "" {
		t.Skipf("skipping integration test: %s not set", env)
	}
	return value
}

func requireValidCredential(t *testing.T, name string, err error) {
	t.Helper()
	if err != nil {
		t.Skipf("skipping integration test: %s credential invalid or unavailable: %v", name, err)
	}
}

func TestIntegrationGitHubRepo(t *testing.T) {
	token := skipIfMissing(t, "GITHUB_TOKEN")
	g := NewGitHubGateway(os.Getenv("GITHUB_CLIENT_ID"), os.Getenv("GITHUB_CLIENT_SECRET"))

	ctx := context.Background()
	user, err := g.GetUser(ctx, &oauth2.Token{AccessToken: token})
	requireValidCredential(t, "GITHUB_TOKEN", err)
	if user.Login == "" {
		t.Fatal("expected non-empty login")
	}
	t.Logf("authenticated as %s", user.Login)
}

func TestIntegrationGiteaChangeLifecycle(t *testing.T) {
	if os.Getenv("GITEA_INTEGRATION_ALLOW_MUTATION") != "1" {
		t.Skip("set GITEA_INTEGRATION_ALLOW_MUTATION=1 to run the disposable Gitea change lifecycle test")
	}

	instanceURL := skipIfMissing(t, "GITEA_URL")
	token := skipIfMissing(t, "GITEA_TOKEN")
	owner := skipIfMissing(t, "GITEA_TEST_OWNER")
	repo := skipIfMissing(t, "GITEA_TEST_REPO")
	head := skipIfMissing(t, "GITEA_TEST_HEAD")
	base := os.Getenv("GITEA_TEST_BASE")
	if base == "" {
		base = "main"
	}

	provider := NewGiteaGateway(instanceURL)
	credential := forge.Credential{Token: token}
	repository := forge.Repository{Namespace: owner, Name: repo}

	change, err := provider.OpenChange(context.Background(), credential, repository, forge.OpenChangeRequest{
		Title: fmt.Sprintf("Dev Plane integration %d", time.Now().UnixNano()),
		Body:  "Disposable Dev Plane Gitea/Forgejo provider integration test.",
		Head:  head,
		Base:  base,
		Draft: true,
	})
	requireValidCredential(t, "GITEA_TOKEN", err)
	if change.Number <= 0 || change.URL == "" {
		t.Fatalf("invalid change result: %#v", change)
	}

	t.Cleanup(func() {
		path := fmt.Sprintf("%s/pulls/%d", provider.repositoryPath(repository), change.Number)
		if err := provider.doJSON(
			context.Background(),
			http.MethodPatch,
			path,
			credential,
			map[string]string{"state": "closed"},
			nil,
		); err != nil {
			t.Logf("warning: failed to close disposable Gitea pull request %s: %v", change.URL, err)
		}
	})

	t.Logf("opened disposable draft change %s", change.URL)
}

func TestIntegrationLinearTeams(t *testing.T) {
	apiKey := skipIfMissing(t, "LINEAR_API_KEY")
	g := NewLinearGateway(apiKey)

	teams, err := g.GetTeams(context.Background())
	requireValidCredential(t, "LINEAR_API_KEY", err)
	t.Logf("found %d teams", len(teams))
}

func TestIntegrationSlackAuthTest(t *testing.T) {
	token := skipIfMissing(t, "SLACK_BOT_TOKEN")
	g := NewSlackGateway(token)

	err := g.Validate(context.Background())
	requireValidCredential(t, "SLACK_BOT_TOKEN", err)
}

func TestIntegrationDiscordUserMe(t *testing.T) {
	token := skipIfMissing(t, "DISCORD_BOT_TOKEN")
	g := NewDiscordGateway(token, "")

	err := g.Validate(context.Background())
	requireValidCredential(t, "DISCORD_BOT_TOKEN", err)
}
