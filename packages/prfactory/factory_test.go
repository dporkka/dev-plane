package prfactory

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/gateway"
)

func TestCreateGitHubPRRequiresGatewayAndToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	factory := NewFactory(nil, nil)

	if _, err := factory.createGitHubPR(context.Background(), "owner", "repo", "title", "body", "head", "main", false); err == nil {
		t.Fatal("expected missing gateway error")
	} else if !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("error = %v, want gateway", err)
	}

	factory.github = &fakeGitHubPRCreator{}
	if _, err := factory.createGitHubPR(context.Background(), "owner", "repo", "title", "body", "head", "main", false); err == nil {
		t.Fatal("expected missing token error")
	} else if !strings.Contains(err.Error(), "token") {
		t.Fatalf("error = %v, want token", err)
	}
}

func TestCreateGitHubPRSendsTokenAndDraftPayload(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	creator := &fakeGitHubPRCreator{result: &gateway.GitHubPR{
		Number:  42,
		HTMLURL: "https://github.com/owner/repo/pull/42",
	}}
	factory := NewFactory(nil, nil).WithGitHubToken("ghp_test")
	factory.github = creator

	pr, err := factory.createGitHubPR(context.Background(), "owner", "repo", "title", "body", "agent/task", "main", true)
	if err != nil {
		t.Fatalf("createGitHubPR() error: %v", err)
	}
	if pr.Number != 42 {
		t.Fatalf("PR number = %d, want 42", pr.Number)
	}
	if creator.token != "ghp_test" {
		t.Fatalf("token = %q, want ghp_test", creator.token)
	}
	if creator.owner != "owner" || creator.name != "repo" {
		t.Fatalf("repo = %s/%s, want owner/repo", creator.owner, creator.name)
	}
	if creator.request.Title != "title" || creator.request.Head != "agent/task" || creator.request.Base != "main" || !creator.request.Draft {
		t.Fatalf("request = %+v", creator.request)
	}
}

type fakeGitHubPRCreator struct {
	token   string
	owner   string
	name    string
	request gateway.NewPR
	result  *gateway.GitHubPR
	err     error
}

func (f *fakeGitHubPRCreator) CreatePR(ctx context.Context, token *oauth2.Token, owner, name string, pr gateway.NewPR) (*gateway.GitHubPR, error) {
	if token != nil {
		f.token = token.AccessToken
	}
	f.owner = owner
	f.name = name
	f.request = pr
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &gateway.GitHubPR{Number: 1, HTMLURL: "https://github.com/owner/repo/pull/1"}, nil
}
