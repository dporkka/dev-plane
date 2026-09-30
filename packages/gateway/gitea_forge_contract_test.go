package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ai-dev-control-plane/forge"
	"github.com/ai-dev-control-plane/forge/contracttest"
)

type giteaContractState struct {
	mu     sync.Mutex
	merged bool
}

func TestGiteaGatewayForgeContract(t *testing.T) {
	state := &giteaContractState{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "token test-token" {
			t.Fatalf("authorization = %q, want token test-token", got)
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/acme/widget/pulls":
			var payload struct {
				Title string `json:"title"`
				Body  string `json:"body"`
				Head  string `json:"head"`
				Base  string `json:"base"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode create payload: %v", err)
			}
			if payload.Title != "WIP: Portable change" {
				t.Fatalf("title = %q, want WIP draft prefix", payload.Title)
			}
			if payload.Head != "feature/portable-change" || payload.Base != "main" {
				t.Fatalf("branches = %q -> %q", payload.Head, payload.Base)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   41,
				"title":    payload.Title,
				"body":     payload.Body,
				"state":    "open",
				"html_url": serverURL(r) + "/acme/widget/pulls/41",
				"draft":    true,
				"merged":   false,
				"head": map[string]any{
					"ref": "feature/portable-change",
					"sha": "head-41",
				},
				"base": map[string]any{
					"ref": "main",
					"sha": "base-1",
				},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/acme/widget/pulls/41/merge":
			var payload struct {
				Do           string `json:"do"`
				HeadCommitID string `json:"head_commit_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode merge payload: %v", err)
			}
			if payload.Do != "squash" {
				t.Fatalf("merge method = %q, want squash", payload.Do)
			}
			state.mu.Lock()
			state.merged = true
			state.mu.Unlock()
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/widget/pulls/41":
			state.mu.Lock()
			merged := state.merged
			state.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":           41,
				"title":            "WIP: Portable change",
				"body":             "Created by forge conformance tests.",
				"state":            "closed",
				"html_url":         serverURL(r) + "/acme/widget/pulls/41",
				"draft":            true,
				"merged":           merged,
				"merge_commit_sha": "merge-41",
				"head": map[string]any{
					"ref": "feature/portable-change",
					"sha": "head-41",
				},
				"base": map[string]any{
					"ref": "main",
					"sha": "base-1",
				},
			})

		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	contracttest.Run(t, func(t *testing.T) contracttest.Fixture {
		return contracttest.Fixture{
			Provider:   NewGiteaGateway(server.URL),
			Credential: forge.Credential{Token: "test-token"},
			Repository: forge.Repository{Namespace: "acme", Name: "widget"},
		}
	})
}

func TestGiteaGatewayMergePassesExpectedHeadRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/merge"):
			var payload struct {
				Do           string `json:"do"`
				HeadCommitID string `json:"head_commit_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Do != "rebase" {
				t.Fatalf("method = %q, want rebase", payload.Do)
			}
			if payload.HeadCommitID != "expected-head" {
				t.Fatalf("head_commit_id = %q, want expected-head", payload.HeadCommitID)
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number": 1, "state": "closed", "merged": true,
				"merge_commit_sha": "merged-head",
				"html_url": "https://gitea.example/acme/widget/pulls/1",
				"head": map[string]any{"ref": "feature", "sha": "expected-head"},
				"base": map[string]any{"ref": "main", "sha": "base"},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewGiteaGateway(server.URL)
	result, err := provider.MergeChange(
		context.Background(),
		forge.Credential{Token: "token"},
		forge.Repository{Namespace: "acme", Name: "widget"},
		1,
		forge.MergeChangeRequest{
			Method:               forge.MergeMethodRebase,
			ExpectedHeadRevision: "expected-head",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != "merged-head" {
		t.Fatalf("revision = %q", result.Revision)
	}
}

func TestGiteaGatewayNormalizesProviderErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		target error
	}{
		{name: "validation", status: http.StatusUnprocessableEntity, target: forge.ErrInvalidRequest},
		{name: "missing", status: http.StatusNotFound, target: forge.ErrNotFound},
		{name: "conflict", status: http.StatusConflict, target: forge.ErrConflict},
		{name: "method not allowed", status: http.StatusMethodNotAllowed, target: forge.ErrConflict},
		{name: "archived", status: http.StatusLocked, target: forge.ErrConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": tt.name})
			}))
			defer server.Close()

			provider := NewGiteaGateway(server.URL)
			_, err := provider.OpenChange(
				context.Background(),
				forge.Credential{Token: "token"},
				forge.Repository{Namespace: "acme", Name: "widget"},
				forge.OpenChangeRequest{Title: "change", Head: "feature", Base: "main"},
			)
			if !errors.Is(err, tt.target) {
				t.Fatalf("error = %v, want %v", err, tt.target)
			}
		})
	}
}

func TestGiteaGatewayRejectsNestedNamespace(t *testing.T) {
	provider := NewGiteaGateway("https://gitea.example")
	_, err := provider.OpenChange(
		context.Background(),
		forge.Credential{},
		forge.Repository{Namespace: "group/subgroup", Name: "widget"},
		forge.OpenChangeRequest{Title: "change", Head: "feature", Base: "main"},
	)
	if !forge.IsInvalidRequest(err) {
		t.Fatalf("error = %v, want invalid request", err)
	}
}

func TestGiteaGatewayCustomDraftPrefix(t *testing.T) {
	var title string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		title = payload.Title
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 1, "title": payload.Title, "state": "open", "draft": true,
			"html_url": "https://gitea.example/acme/widget/pulls/1",
			"head": map[string]any{"ref": "feature", "sha": "head"},
			"base": map[string]any{"ref": "main", "sha": "base"},
		})
	}))
	defer server.Close()

	provider := NewGiteaGateway(server.URL).WithDraftTitlePrefix("[Draft] ")
	_, err := provider.OpenChange(
		context.Background(),
		forge.Credential{Token: "token"},
		forge.Repository{Namespace: "acme", Name: "widget"},
		forge.OpenChangeRequest{Title: "change", Head: "feature", Base: "main", Draft: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if title != "[Draft] change" {
		t.Fatalf("title = %q", title)
	}
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
