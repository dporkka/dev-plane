package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testAppPrivateKey(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

func TestGitHubAppTokenSourceRestrictsRepositoryAndPermissions(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("no app identity")
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/teamA/app/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 987})
		case r.Method == "POST" && r.URL.Path == "/app/installations/987/access_tokens":
			var body struct {
				Repositories []string          `json:"repositories"`
				Permissions  map[string]string `json:"permissions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if len(body.Repositories) != 1 || body.Repositories[0] != "app" {
				t.Errorf("unscoped repositories: %#v", body.Repositories)
			}
			if len(body.Permissions) != 1 || body.Permissions["deployments"] != "write" {
				t.Errorf("unexpected permission escalation: %#v", body.Permissions)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"token":        "ghs-test-not-loggable",
				"expires_at":   time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
				"repositories": []map[string]any{{"full_name": "teamA/app"}},
				"permissions":  map[string]string{"deployments": "write", "metadata": "read"},
			})
		default:
			t.Errorf("unexpected path %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	provider, err := NewGitHubAppTokenSource("123", testAppPrivateKey(t))
	if err != nil {
		t.Fatal(err)
	}
	provider.apiBaseURL = server.URL
	provider.httpClient = server.Client()
	token, err := provider.Token(context.Background(), "teamA", "app", map[string]string{"deployments": "write"})
	if err != nil {
		t.Fatal(err)
	}
	if token != "ghs-test-not-loggable" || calls != 2 {
		t.Fatalf("token fetched incorrectly, calls=%d", calls)
	}
}

func TestGitHubAppTokenSourceRejectsMisScopedResponse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repos []string
		perms map[string]string
	}{
		{"other repository", []string{"other/repo"}, map[string]string{"contents": "write"}},
		{"multiple repositories", []string{"teamA/app", "other/repo"}, map[string]string{"contents": "write"}},
		{"extra privilege", []string{"teamA/app"}, map[string]string{"contents": "write", "deployments": "write"}},
		{"missing privilege", []string{"teamA/app"}, map[string]string{"contents": "read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					json.NewEncoder(w).Encode(map[string]any{"id": 987})
					return
				}
				var repos []map[string]any
				for _, name := range tc.repos {
					repos = append(repos, map[string]any{"full_name": name})
				}
				json.NewEncoder(w).Encode(map[string]any{"token": "overpowered-secret", "expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339), "repositories": repos, "permissions": tc.perms})
			}))
			defer server.Close()
			provider, _ := NewGitHubAppTokenSource("123", testAppPrivateKey(t))
			provider.apiBaseURL = server.URL
			provider.httpClient = server.Client()
			token, err := provider.Token(context.Background(), "teamA", "app", map[string]string{"contents": "write"})
			if err == nil || token != "" {
				t.Fatalf("must deny unexpected scope, token_len=%d", len(token))
			}
			if strings.Contains(err.Error(), "overpowered-secret") {
				t.Fatal("secret leaked in error")
			}
		})
	}
}
func TestGitHubAppTokenSourceFailsClosed(t *testing.T) {
	key := testAppPrivateKey(t)
	for _, tc := range []struct{ id, key string }{{"", key}, {"123", "garbage"}} {
		if _, err := NewGitHubAppTokenSource(tc.id, tc.key); err == nil {
			t.Fatal("invalid app config accepted")
		}
	}
	p, err := NewGitHubAppTokenSource("123", key)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ owner, repo string }{{"", "repo"}, {"some/evil", "repo"}, {"team", "../evil"}, {"team", ""}} {
		if _, err := p.Token(context.Background(), tc.owner, tc.repo, map[string]string{"contents": "write"}); err == nil {
			t.Errorf("accepted malformed path %q/%q", tc.owner, tc.repo)
		}
	}
	if _, err := p.Token(context.Background(), "team", "repo", map[string]string{}); err == nil {
		t.Error("empty permission set accepted")
	}
}
