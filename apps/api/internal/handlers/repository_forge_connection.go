package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/models"
)

// ForgeRepositoryConnectionRequest is the provider-neutral repository identity
// accepted by the connection normalizer. API wiring remains separate so this
// contract can be reused by HTTP, CLI, and import surfaces.
type ForgeRepositoryConnectionRequest struct {
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
}

type normalizedRepositoryConnection struct {
	Owner    string
	Name     string
	FullName string
	CloneURL string
	Settings json.RawMessage
}

func normalizeRepositoryConnection(req ForgeRepositoryConnectionRequest) (normalizedRepositoryConnection, error) {
	req.Owner = strings.TrimSpace(req.Owner)
	req.Name = strings.TrimSpace(req.Name)
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	if req.Provider == "" {
		req.Provider = string(models.ForgeProviderGitHub)
	}
	if req.Owner == "" || req.Name == "" {
		return normalizedRepositoryConnection{}, errors.New("owner and name are required")
	}

	forge := models.RepositoryForgeSettings{
		Provider: models.ForgeProvider(req.Provider),
		BaseURL:  req.BaseURL,
	}
	settings, err := models.PutRepositoryForgeSettings(nil, forge)
	if err != nil {
		return normalizedRepositoryConnection{}, err
	}
	forge, err = models.ParseRepositoryForgeSettings(settings)
	if err != nil {
		return normalizedRepositoryConnection{}, err
	}

	switch forge.Provider {
	case models.ForgeProviderGitHub:
		if err := validateGitHubOwner(req.Owner); err != nil {
			return normalizedRepositoryConnection{}, err
		}
		if err := validateGitHubRepoName(req.Name); err != nil {
			return normalizedRepositoryConnection{}, err
		}
	case models.ForgeProviderGitea:
		if err := validateForgePathSegment("owner", req.Owner); err != nil {
			return normalizedRepositoryConnection{}, err
		}
		if err := validateForgePathSegment("repository name", req.Name); err != nil {
			return normalizedRepositoryConnection{}, err
		}
	default:
		return normalizedRepositoryConnection{}, fmt.Errorf("unsupported forge provider %q", forge.Provider)
	}

	fullName := req.Owner + "/" + req.Name
	cloneBase := "https://github.com"
	if forge.Provider == models.ForgeProviderGitea {
		cloneBase = forge.BaseURL
	}
	return normalizedRepositoryConnection{
		Owner:    req.Owner,
		Name:     req.Name,
		FullName: fullName,
		CloneURL: strings.TrimRight(cloneBase, "/") + "/" + fullName + ".git",
		Settings: settings,
	}, nil
}

func validateForgePathSegment(label, value string) error {
	if value == "." || value == ".." {
		return fmt.Errorf("%s is invalid", label)
	}
	if len(value) > 255 {
		return fmt.Errorf("%s must be 255 characters or fewer", label)
	}
	if strings.Contains(value, "/") || strings.Contains(value, "\\") {
		return fmt.Errorf("%s cannot contain path separators", label)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("%s may only contain letters, numbers, dots, hyphens, and underscores", label)
	}
	return nil
}
