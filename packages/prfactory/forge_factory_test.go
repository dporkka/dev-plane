package prfactory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/ai-dev-control-plane/gateway"
)

type recordingForge struct {
	credential gateway.ForgeCredential
	owner      string
	name       string
	request    gateway.ForgeNewPullRequest
}

func (f *recordingForge) Name() string { return "recording" }
func (f *recordingForge) ListRepositories(context.Context, gateway.ForgeCredential, int) ([]gateway.ForgeRepository, error) {
	return nil, nil
}
func (f *recordingForge) GetRepository(context.Context, gateway.ForgeCredential, string, string) (*gateway.ForgeRepository, error) {
	return nil, nil
}
func (f *recordingForge) CreatePullRequest(_ context.Context, credential gateway.ForgeCredential, owner, name string, pr gateway.ForgeNewPullRequest) (*gateway.ForgePullRequest, error) {
	f.credential = credential
	f.owner = owner
	f.name = name
	f.request = pr
	return &gateway.ForgePullRequest{Number: 12, HTMLURL: "https://forge.example/acme/app/pulls/12", Draft: pr.Draft}, nil
}
func (f *recordingForge) MergePullRequest(context.Context, gateway.ForgeCredential, string, string, int, gateway.ForgeMergeRequest) (*gateway.ForgeMergeResult, error) {
	return nil, nil
}
func (f *recordingForge) CreateWebhook(context.Context, gateway.ForgeCredential, string, string, string, string) (int64, error) {
	return 0, nil
}
func (f *recordingForge) DeleteWebhook(context.Context, gateway.ForgeCredential, string, string, int64) error {
	return nil
}

func TestWithForgeSeparatesAPIFromGitPushCredential(t *testing.T) {
	forge := &recordingForge{}
	factory := NewFactory(nil, nil).
		WithForge(forge, gateway.ForgeCredential{AccessToken: "api-token"}).
		WithGitPushCredential(GitPushCredential{Username: "git-user", Password: "git-password"})

	if factory.forge != forge {
		t.Fatal("forge was not configured")
	}
	if factory.forgeCredential.AccessToken != "api-token" {
		t.Fatalf("forge token = %q", factory.forgeCredential.AccessToken)
	}
	if factory.gitPushCredential.Username != "git-user" || factory.gitPushCredential.Password != "git-password" {
		t.Fatalf("git push credential = %+v", factory.gitPushCredential)
	}
}

func TestCreateForgePullRequestUsesForgeCredential(t *testing.T) {
	forge := &recordingForge{}
	factory := NewFactory(nil, nil).
		WithForge(forge, gateway.ForgeCredential{AccessToken: "api-token"})

	created, err := factory.createForgePR(context.Background(), "acme", "app", "Agent change", "Evidence", "agent/change", "main", true)
	if err != nil {
		t.Fatalf("createForgePR: %v", err)
	}
	if created.Number != 12 || created.HTMLURL == "" || !created.Draft {
		t.Fatalf("created = %+v", created)
	}
	if forge.credential.AccessToken != "api-token" {
		t.Fatalf("forge received credential %q", forge.credential.AccessToken)
	}
	if forge.owner != "acme" || forge.name != "app" {
		t.Fatalf("repository = %s/%s", forge.owner, forge.name)
	}
	if !forge.request.Draft || forge.request.Head != "agent/change" || forge.request.Base != "main" {
		t.Fatalf("request = %+v", forge.request)
	}
}

func TestConfigureGitAskPassCredentialUsesProviderNeutralUsername(t *testing.T) {
	cmd := &exec.Cmd{}
	cleanup, err := configureGitAskPassCredential(cmd, GitPushCredential{Username: "david", Password: "gitea-token"})
	if err != nil {
		t.Fatalf("configureGitAskPassCredential: %v", err)
	}
	defer cleanup()

	if got := getEnv(cmd, "DEV_PLANE_GIT_USERNAME"); got != "david" {
		t.Fatalf("username env = %q", got)
	}
	if got := getEnv(cmd, "DEV_PLANE_GIT_PASSWORD"); got != "gitea-token" {
		t.Fatalf("password env = %q", got)
	}
	if getEnv(cmd, "GITHUB_TOKEN") != "" {
		t.Fatal("generic Git credential leaked through GITHUB_TOKEN")
	}
}

func TestWithGitHubTokenPreservesLegacyGitHubPushConvention(t *testing.T) {
	factory := NewFactory(nil, nil).WithGitHubToken(" token ")
	if factory.forgeCredential.AccessToken != "token" {
		t.Fatalf("forge token = %q", factory.forgeCredential.AccessToken)
	}
	if factory.gitPushCredential.Username != "x-access-token" || factory.gitPushCredential.Password != "token" {
		t.Fatalf("git push credential = %+v", factory.gitPushCredential)
	}
}

func TestWithGitHubGatewayWrapsGatewayAsForge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	factory := NewFactory(nil, nil).WithGitHubGateway(gateway.NewGitHubGateway("id", "secret"))
	if factory.forge == nil || factory.forge.Name() != "github" {
		t.Fatalf("forge = %#v", factory.forge)
	}
}
