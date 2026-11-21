package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/config"
)

func TestGitHubAuthRedirect_UsesExplicitTrustedCallback(t *testing.T) {
	const callback = "https://review-staging.example.invalid/api/auth/github/callback"
	h := NewGitHubAuthHandler(nil, &config.Config{
		GitHubClientID:    "review-staging-oauth-client",
		GitHubRedirectURL: callback,
		OAuthCookieSecure: true,
	})
	rec := httptest.NewRecorder()
	h.GitHubAuthRedirect(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/github?redirect_uri=https://attacker.invalid/callback", nil))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := location.Query().Get("redirect_uri"); got != callback {
		t.Fatalf("redirect_uri=%q; want trusted %q", got, callback)
	}
}

func TestGitHubAuthRedirect_SetsLaxStateCookie(t *testing.T) {
	h := NewGitHubAuthHandler(nil, &config.Config{
		GitHubClientID:    "review-staging-oauth-client",
		OAuthCookieSecure: true,
	})
	rec := httptest.NewRecorder()
	h.GitHubAuthRedirect(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/github", nil))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	cookie := rec.Header().Get("Set-Cookie")
	for _, field := range []string{"oauth_state=", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(cookie, field) {
			t.Errorf("state cookie lacks %s: %q", field, cookie)
		}
	}
	if strings.Contains(cookie, "SameSite=Strict") {
		t.Errorf("Strict state cookie cannot survive cross-site GitHub callback")
	}
}
