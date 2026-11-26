package prfactory

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRuntimeFactoryInvalidConfigFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, provider, url, token, user, password string
	}{
		{name: "unknown provider", provider: "synthetic-secret"},
		{name: "missing URL", provider: "gitea", token: "synthetic-secret"},
		{name: "missing token", provider: "gitea", url: "https://forge.example"},
		{name: "embedded URL credential", provider: "gitea", url: "https://user:synthetic-secret@forge.example", token: "api-token"},
		{name: "partial Git credentials", provider: "gitea", url: "https://forge.example", token: "api-token", password: "synthetic-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FORGE_PROVIDER", tc.provider)
			t.Setenv("GITHUB_TOKEN", "github-must-not-be-used")
			t.Setenv("GITEA_URL", tc.url)
			t.Setenv("GITEA_TOKEN", tc.token)
			t.Setenv("GITEA_GIT_USERNAME", tc.user)
			t.Setenv("GITEA_GIT_PASSWORD", tc.password)
			f := NewFactory(nil, nil)
			_, err := f.CreatePullRequest(context.Background(), "task")
			if !errors.Is(err, ErrForgeConfiguration) || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("expected sanitized runtime configuration error before database/network access")
			}
			if f.forge != nil || f.github != nil || f.forgeCredential.AccessToken != "" || f.gitPushCredential != (GitPushCredential{}) {
				t.Fatal("invalid config fell back to a credential-bearing forge")
			}
		})
	}
}
