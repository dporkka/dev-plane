//go:build integration

package gateway

import (
	"context"
	"os"
	"testing"

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
