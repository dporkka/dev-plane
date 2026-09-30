package gateway

import "testing"

func TestSelectForgeGitea(t *testing.T) {
	selection, err := SelectForge(ForgeSettings{
		Provider:              "gitea",
		GitRemote:             "review",
		GiteaURL:              "https://code.example.com",
		GiteaToken:            "secret-token",
		GiteaUsername:         "alice",
		GiteaDraftTitlePrefix: "[Draft]",
	})
	if err != nil {
		t.Fatalf("SelectForge() error: %v", err)
	}
	if selection == nil {
		t.Fatal("selection is nil")
	}
	if selection.Provider.Name() != "gitea" {
		t.Fatalf("provider = %q, want gitea", selection.Provider.Name())
	}
	if selection.Credential.Token != "secret-token" {
		t.Fatalf("token = %q", selection.Credential.Token)
	}
	if selection.Publisher == nil {
		t.Fatal("publisher is nil")
	}
	if selection.Remote != "review" {
		t.Fatalf("remote = %q, want review", selection.Remote)
	}
}

func TestSelectForgeForgejoAlias(t *testing.T) {
	selection, err := SelectForge(ForgeSettings{
		Provider:  "forgejo",
		GiteaURL:  "https://forge.example.com",
		GitRemote: "origin",
	})
	if err != nil {
		t.Fatalf("SelectForge() error: %v", err)
	}
	if selection == nil || selection.Provider.Name() != "gitea" {
		t.Fatalf("selection = %#v, want Gitea-compatible provider", selection)
	}
}

func TestSelectForgeGitHubRequiresTokenForDefaultIntegration(t *testing.T) {
	selection, err := SelectForge(ForgeSettings{Provider: "github"})
	if err != nil {
		t.Fatalf("SelectForge() error: %v", err)
	}
	if selection != nil {
		t.Fatalf("selection = %#v, want disabled GitHub integration", selection)
	}
}

func TestSelectForgeRejectsGiteaWithoutURL(t *testing.T) {
	_, err := SelectForge(ForgeSettings{Provider: "gitea"})
	if err == nil {
		t.Fatal("expected missing Gitea URL error")
	}
}

func TestSelectForgeRejectsUnknownProvider(t *testing.T) {
	_, err := SelectForge(ForgeSettings{Provider: "bitbucket"})
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestSelectForgeDefaultsRemoteToOrigin(t *testing.T) {
	selection, err := SelectForge(ForgeSettings{
		Provider:    "gitea",
		GiteaURL:   "https://code.example.com",
		GiteaToken: "token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Remote != "origin" {
		t.Fatalf("remote = %q, want origin", selection.Remote)
	}
}
