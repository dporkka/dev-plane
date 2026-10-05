package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubForgeReturnsRequestedDraftState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GitHubPR{
			ID:      1,
			Number:  7,
			Title:   "Draft change",
			HTMLURL: "https://github.example/acme/widget/pull/7",
		})
	}))
	defer server.Close()

	forge := NewGitHubForge(testGateway(server))
	pr, err := forge.CreatePullRequest(context.Background(), ForgeCredential{AccessToken: "secret"}, "acme", "widget", ForgeNewPullRequest{
		Title: "Draft change",
		Head:  "agent/draft-change",
		Base:  "main",
		Draft: true,
	})
	if err != nil {
		t.Fatalf("create pull request: %v", err)
	}
	if !pr.Draft {
		t.Fatal("normalized pull request lost requested draft state")
	}
}
