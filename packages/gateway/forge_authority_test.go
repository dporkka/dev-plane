package gateway

import "testing"

func TestGiteaForgeAuthorityUsesCanonicalInstanceRoot(t *testing.T) {
	forge, err := NewGiteaForge("HTTPS://Git.Example.Test/subpath/api/v1/", nil)
	if err != nil {
		t.Fatalf("new gitea forge: %v", err)
	}
	got := forge.Authority()
	if got.Provider != "gitea" || got.BaseURL != "https://git.example.test/subpath" {
		t.Fatalf("authority = %+v", got)
	}
}

func TestGitHubForgeAuthorityIsCanonicalGitHubOrigin(t *testing.T) {
	got := NewGitHubForge(nil).Authority()
	if got.Provider != "github" || got.BaseURL != "https://github.com" {
		t.Fatalf("authority = %+v", got)
	}
}
