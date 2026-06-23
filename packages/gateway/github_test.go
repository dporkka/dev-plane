package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestNewGitHubGateway(t *testing.T) {
	g := NewGitHubGateway("client-id", "client-secret")
	if g == nil {
		t.Fatal("expected gateway")
	}
	if g.clientID != "client-id" {
		t.Errorf("client id = %q", g.clientID)
	}
	if g.oauthConfig == nil {
		t.Fatal("expected oauth config")
	}
}

func TestSetRedirectURL(t *testing.T) {
	g := NewGitHubGateway("id", "secret")
	g.SetRedirectURL("http://localhost:3000/callback")
	if g.oauthConfig.RedirectURL != "http://localhost:3000/callback" {
		t.Errorf("redirect url = %q", g.oauthConfig.RedirectURL)
	}
}

func TestAuthURL(t *testing.T) {
	g := NewGitHubGateway("id", "secret")
	g.SetRedirectURL("http://localhost/callback")
	url := g.AuthURL("state-123")
	if !strings.Contains(url, "client_id=id") {
		t.Errorf("auth url missing client_id: %q", url)
	}
	if !strings.Contains(url, "scope=repo") {
		t.Errorf("auth url missing scope: %q", url)
	}
	if !strings.Contains(url, "state=state-123") {
		t.Errorf("auth url missing state: %q", url)
	}
}

func TestParseInstallationID(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		err   bool
	}{
		{"123", 123, false},
		{"0", 0, false},
		{"-5", -5, false},
		{"abc", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseInstallationID(tt.input)
			if (err != nil) != tt.err {
				t.Errorf("error = %v, wantErr %v", err, tt.err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCreatePR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/repos/owner/repo/pulls" {
			t.Errorf("path = %q", r.URL.Path)
		}

		var pr NewPR
		if err := json.NewDecoder(r.Body).Decode(&pr); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if pr.Title != "title" || pr.Head != "feature" || pr.Base != "main" || !pr.Draft {
			t.Errorf("pr = %+v", pr)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GitHubPR{
			ID:      1,
			Number:  42,
			Title:   "title",
			HTMLURL: "https://github.com/owner/repo/pull/42",
		})
	}))
	defer server.Close()

	g := testGateway(server)

	pr, err := g.CreatePR(context.Background(), &oauth2.Token{AccessToken: "token"}, "owner", "repo", NewPR{
		Title: "title",
		Body:  "body",
		Head:  "feature",
		Base:  "main",
		Draft: true,
	})
	if err != nil {
		t.Fatalf("create pr: %v", err)
	}
	if pr.Number != 42 {
		t.Errorf("number = %d, want 42", pr.Number)
	}
}

func TestGetRepo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GitHubRepo{
			ID:            1,
			Name:          "repo",
			FullName:      "owner/repo",
			DefaultBranch: "main",
			CloneURL:      "https://github.com/owner/repo.git",
		})
	}))
	defer server.Close()

	g := testGateway(server)

	repo, err := g.GetRepo(context.Background(), &oauth2.Token{AccessToken: "token"}, "owner", "repo")
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if repo.FullName != "owner/repo" {
		t.Errorf("full_name = %q", repo.FullName)
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("default branch = %q", repo.DefaultBranch)
	}
}

func TestListRepos(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]GitHubRepo{
			{ID: 1, FullName: "owner/a"},
			{ID: 2, FullName: "owner/b"},
		})
	}))
	defer server.Close()

	g := testGateway(server)

	repos, err := g.ListRepos(context.Background(), &oauth2.Token{AccessToken: "token"}, 1)
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 2 {
		t.Errorf("repos = %d, want 2", len(repos))
	}
}

func TestCreateWebhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/hooks" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"id": 99})
	}))
	defer server.Close()

	g := testGateway(server)

	id, err := g.CreateWebhook(context.Background(), &oauth2.Token{AccessToken: "token"}, "owner", "repo", "https://example.com/webhook", "secret")
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	if id != 99 {
		t.Errorf("id = %d, want 99", id)
	}
}

func TestDeleteWebhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != "/repos/owner/repo/hooks/77" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	g := testGateway(server)

	if err := g.DeleteWebhook(context.Background(), &oauth2.Token{AccessToken: "token"}, "owner", "repo", 77); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
}

func TestGitHubAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("bad credentials"))
	}))
	defer server.Close()

	g := testGateway(server)

	_, err := g.GetRepo(context.Background(), &oauth2.Token{AccessToken: "token"}, "owner", "repo")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "github API error 401") {
		t.Errorf("error = %v", err)
	}
}

func testGateway(server *httptest.Server) *GitHubGateway {
	g := NewGitHubGateway("id", "secret")
	g.httpClient = server.Client()
	g.apiBaseURL = server.URL
	g.oauthConfig.Endpoint = oauth2.Endpoint{
		AuthURL:  server.URL + "/authorize",
		TokenURL: server.URL + "/token",
	}
	g.oauthConfig.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	return g
}
