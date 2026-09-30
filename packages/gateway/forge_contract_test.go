package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/forge"
	"github.com/ai-dev-control-plane/forge/contracttest"
)

func TestGitHubGatewayForgeContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/pulls":
			var payload NewPR
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode open payload: %v", err)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Fatalf("authorization = %q, want Bearer test-token", got)
			}
			_ = json.NewEncoder(w).Encode(GitHubPR{
				Number:  41,
				Title:   payload.Title,
				Body:    payload.Body,
				State:   "open",
				HTMLURL: "https://github.example/acme/widget/pull/41",
				Head: struct {
					Ref string `json:"ref"`
					SHA string `json:"sha"`
				}{Ref: payload.Head, SHA: "head-41"},
				Base: struct {
					Ref string `json:"ref"`
					SHA string `json:"sha"`
				}{Ref: payload.Base, SHA: "base-1"},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/repos/acme/widget/pulls/41/merge":
			var payload MergePRRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode merge payload: %v", err)
			}
			if payload.Method != "squash" {
				t.Fatalf("merge method = %q, want squash", payload.Method)
			}
			_ = json.NewEncoder(w).Encode(MergePRResult{
				SHA:     "merge-41",
				Merged:  true,
				Message: "merged",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	contracttest.Run(t, func(t *testing.T) contracttest.Fixture {
		gateway := NewGitHubGateway("id", "secret")
		gateway.apiBaseURL = server.URL
		return contracttest.Fixture{
			Provider:   gateway,
			Credential: forge.Credential{Token: "test-token"},
			Repository: forge.Repository{Owner: "acme", Name: "widget"},
		}
	})
}
