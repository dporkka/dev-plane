package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestGiteaForgeGetRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/acme/widget" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "token secret" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             7,
			"name":           "widget",
			"full_name":      "acme/widget",
			"description":    "agent test repo",
			"private":        true,
			"clone_url":      "https://git.example/acme/widget.git",
			"ssh_url":        "git@git.example:acme/widget.git",
			"html_url":       "https://git.example/acme/widget",
			"default_branch": "main",
		})
	}))
	defer server.Close()

	forge, err := NewGiteaForge(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}

	repo, err := forge.GetRepository(context.Background(), ForgeCredential{AccessToken: "secret"}, "acme", "widget")
	if err != nil {
		t.Fatalf("get repository: %v", err)
	}
	if repo.FullName != "acme/widget" || repo.DefaultBranch != "main" || !repo.Private {
		t.Fatalf("repository = %+v", repo)
	}
}

func TestGiteaForgeListRepositoriesUsesStablePageSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/user/repos" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("page"); got != "2" {
			t.Fatalf("page = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Fatalf("limit = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "name": "a", "full_name": "acme/a", "default_branch": "main"},
			{"id": 2, "name": "b", "full_name": "acme/b", "default_branch": "trunk"},
		})
	}))
	defer server.Close()

	forge, err := NewGiteaForge(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}
	repos, err := forge.ListRepositories(context.Background(), ForgeCredential{AccessToken: "secret"}, 2)
	if err != nil {
		t.Fatalf("list repositories: %v", err)
	}
	if len(repos) != 2 || repos[1].DefaultBranch != "trunk" {
		t.Fatalf("repositories = %+v", repos)
	}
}

func TestGiteaForgeCreatePullRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/acme/widget/pulls" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		want := map[string]any{
			"title": "Fix race",
			"body":  "Evidence attached",
			"head":  "agent/fix-race",
			"base":  "main",
		}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("body = %#v, want %#v", body, want)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       22,
			"number":   17,
			"title":    "Fix race",
			"body":     "Evidence attached",
			"state":    "open",
			"html_url": "https://git.example/acme/widget/pulls/17",
			"draft":    false,
			"head": map[string]any{
				"ref": "agent/fix-race",
				"sha": "abc123",
			},
			"base": map[string]any{
				"ref": "main",
				"sha": "def456",
			},
		})
	}))
	defer server.Close()

	forge, err := NewGiteaForge(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}
	pr, err := forge.CreatePullRequest(context.Background(), ForgeCredential{AccessToken: "secret"}, "acme", "widget", ForgeNewPullRequest{
		Title: "Fix race",
		Body:  "Evidence attached",
		Head:  "agent/fix-race",
		Base:  "main",
	})
	if err != nil {
		t.Fatalf("create pull request: %v", err)
	}
	if pr.Number != 17 || pr.Head.SHA != "abc123" || pr.Base.Ref != "main" {
		t.Fatalf("pull request = %+v", pr)
	}
}

func TestGiteaForgeRejectsDraftPullRequestBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	forge, err := NewGiteaForge(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}
	_, err = forge.CreatePullRequest(context.Background(), ForgeCredential{AccessToken: "secret"}, "acme", "widget", ForgeNewPullRequest{
		Title: "Draft change",
		Head:  "agent/draft",
		Base:  "main",
		Draft: true,
	})
	if !errors.Is(err, ErrUnsupportedForgeCapability) {
		t.Fatalf("error = %v, want ErrUnsupportedForgeCapability", err)
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestGiteaForgeMergePullRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/acme/widget/pulls/17/merge" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		want := map[string]any{
			"do":                  "squash",
			"head_commit_id":      "abc123",
			"merge_title_field":   "Verified change",
			"merge_message_field": "All gates passed",
		}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("body = %#v, want %#v", body, want)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	forge, err := NewGiteaForge(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}
	result, err := forge.MergePullRequest(context.Background(), ForgeCredential{AccessToken: "secret"}, "acme", "widget", 17, ForgeMergeRequest{
		Method:          "squash",
		Title:           "Verified change",
		Message:         "All gates passed",
		ExpectedHeadSHA: "abc123",
	})
	if err != nil {
		t.Fatalf("merge pull request: %v", err)
	}
	if !result.Merged {
		t.Fatalf("merged = false, want true")
	}
}

func TestGiteaForgeRejectsUnknownMergeMethodBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	forge, err := NewGiteaForge(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}
	_, err = forge.MergePullRequest(context.Background(), ForgeCredential{AccessToken: "secret"}, "acme", "widget", 17, ForgeMergeRequest{Method: "octopus"})
	if err == nil {
		t.Fatal("expected merge method error")
	}
	if requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func TestGiteaForgeWebhookLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/acme/widget/hooks":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode webhook body: %v", err)
			}
			if body["type"] != "gitea" || body["active"] != true {
				t.Fatalf("webhook body = %#v", body)
			}
			config, ok := body["config"].(map[string]any)
			if !ok || config["url"] != "https://control.example/hooks/forge" || config["content_type"] != "json" || config["secret"] != "hook-secret" {
				t.Fatalf("webhook config = %#v", body["config"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 91})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/repos/acme/widget/hooks/91":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	forge, err := NewGiteaForge(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}
	id, err := forge.CreateWebhook(context.Background(), ForgeCredential{AccessToken: "secret"}, "acme", "widget", "https://control.example/hooks/forge", "hook-secret")
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	if id != 91 {
		t.Fatalf("webhook id = %d, want 91", id)
	}
	if err := forge.DeleteWebhook(context.Background(), ForgeCredential{AccessToken: "secret"}, "acme", "widget", 91); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
}

func TestGitHubForgePreservesDraftSemantics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body NewPR
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode github body: %v", err)
		}
		if !body.Draft || body.Head != "agent/change" || body.Base != "main" {
			t.Fatalf("github body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GitHubPR{ID: 1, Number: 5, Title: body.Title, HTMLURL: "https://github.example/acme/widget/pull/5"})
	}))
	defer server.Close()

	forge := NewGitHubForge(testGateway(server))
	pr, err := forge.CreatePullRequest(context.Background(), ForgeCredential{AccessToken: "secret"}, "acme", "widget", ForgeNewPullRequest{
		Title: "Agent change",
		Head:  "agent/change",
		Base:  "main",
		Draft: true,
	})
	if err != nil {
		t.Fatalf("create pull request: %v", err)
	}
	if pr.Number != 5 {
		t.Fatalf("pull request number = %d, want 5", pr.Number)
	}
}
