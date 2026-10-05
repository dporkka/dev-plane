package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/models"
)

func TestNormalizeRepositoryConnectionDefaultsToGitHub(t *testing.T) {
	connection, err := normalizeRepositoryConnection(ConnectRepositoryRequest{Owner: "acme", Name: "widget"})
	if err != nil {
		t.Fatalf("normalize connection: %v", err)
	}
	if connection.CloneURL != "https://github.com/acme/widget.git" {
		t.Fatalf("clone url = %q", connection.CloneURL)
	}
	forge, err := models.ParseRepositoryForgeSettings(connection.Settings)
	if err != nil {
		t.Fatalf("parse forge settings: %v", err)
	}
	if forge.Provider != models.ForgeProviderGitHub || forge.BaseURL != "" {
		t.Fatalf("forge settings = %+v", forge)
	}
}

func TestNormalizeRepositoryConnectionSupportsGiteaSubpath(t *testing.T) {
	connection, err := normalizeRepositoryConnection(ConnectRepositoryRequest{
		Owner:    "acme_team",
		Name:     "widget",
		Provider: "gitea",
		BaseURL:  "https://git.example.test/code/",
	})
	if err != nil {
		t.Fatalf("normalize connection: %v", err)
	}
	if connection.CloneURL != "https://git.example.test/code/acme_team/widget.git" {
		t.Fatalf("clone url = %q", connection.CloneURL)
	}
	forge, err := models.ParseRepositoryForgeSettings(connection.Settings)
	if err != nil {
		t.Fatalf("parse forge settings: %v", err)
	}
	if forge.Provider != models.ForgeProviderGitea || forge.BaseURL != "https://git.example.test/code" {
		t.Fatalf("forge settings = %+v", forge)
	}
}

func TestNormalizeRepositoryConnectionRejectsUnknownProvider(t *testing.T) {
	_, err := normalizeRepositoryConnection(ConnectRepositoryRequest{Owner: "acme", Name: "widget", Provider: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unsupported forge provider") {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizeRepositoryConnectionRejectsGiteaWithoutBaseURL(t *testing.T) {
	_, err := normalizeRepositoryConnection(ConnectRepositoryRequest{Owner: "acme", Name: "widget", Provider: "gitea"})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizeRepositoryConnectionRejectsCustomGitHubBaseURL(t *testing.T) {
	_, err := normalizeRepositoryConnection(ConnectRepositoryRequest{
		Owner:    "acme",
		Name:     "widget",
		Provider: "github",
		BaseURL:  "https://github.enterprise.test",
	})
	if err == nil || !strings.Contains(err.Error(), "custom GitHub base_url") {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizeRepositoryConnectionStoresNoCredentials(t *testing.T) {
	connection, err := normalizeRepositoryConnection(ConnectRepositoryRequest{
		Owner:    "acme",
		Name:     "widget",
		Provider: "gitea",
		BaseURL:  "https://git.example.test",
	})
	if err != nil {
		t.Fatalf("normalize connection: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(connection.Settings, &decoded); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	encoded := string(connection.Settings)
	for _, forbidden := range []string{"token", "password", "secret", "credential"} {
		if strings.Contains(strings.ToLower(encoded), forbidden) {
			t.Fatalf("repository settings unexpectedly contain %q: %s", forbidden, encoded)
		}
	}
}
