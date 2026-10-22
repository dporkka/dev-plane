package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ForgeProvider identifies the repository hosting service that is authoritative
// for repository API operations. Git transport remains a separate concern.
type ForgeProvider string

const (
	ForgeProviderGitHub ForgeProvider = "github"
	ForgeProviderGitea  ForgeProvider = "gitea"
)

// RepositoryForgeSettings is stored under repositories.settings.forge. It
// intentionally contains no credentials; secrets belong in the secret store.
type RepositoryForgeSettings struct {
	Provider ForgeProvider `json:"provider"`
	BaseURL  string        `json:"base_url,omitempty"`
}

// ParseRepositoryForgeSettings returns the forge authority encoded in a
// repository settings document. Existing rows without an explicit forge block
// retain backward-compatible GitHub semantics.
func ParseRepositoryForgeSettings(raw json.RawMessage) (RepositoryForgeSettings, error) {
	settings := RepositoryForgeSettings{Provider: ForgeProviderGitHub}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return settings, nil
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return RepositoryForgeSettings{}, fmt.Errorf("decode repository settings: %w", err)
	}
	forgeRaw, ok := root["forge"]
	if !ok || len(bytes.TrimSpace(forgeRaw)) == 0 || bytes.Equal(bytes.TrimSpace(forgeRaw), []byte("null")) {
		return settings, nil
	}

	var decoded struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url,omitempty"`
	}
	if err := json.Unmarshal(forgeRaw, &decoded); err != nil {
		return RepositoryForgeSettings{}, fmt.Errorf("decode repository forge settings: %w", err)
	}
	provider := strings.ToLower(strings.TrimSpace(decoded.Provider))
	if provider == "" {
		provider = string(ForgeProviderGitHub)
	}
	return normalizeRepositoryForgeSettings(RepositoryForgeSettings{
		Provider: ForgeProvider(provider),
		BaseURL:  decoded.BaseURL,
	})
}

// PutRepositoryForgeSettings merges forge settings into the repository settings
// document without discarding unrelated runtime, feature, or project metadata.
func PutRepositoryForgeSettings(raw json.RawMessage, forge RepositoryForgeSettings) (json.RawMessage, error) {
	normalized, err := normalizeRepositoryForgeSettings(forge)
	if err != nil {
		return nil, err
	}

	root := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, fmt.Errorf("decode repository settings: %w", err)
		}
	}
	forgeRaw, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("encode repository forge settings: %w", err)
	}
	root["forge"] = forgeRaw
	updated, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode repository settings: %w", err)
	}
	return updated, nil
}

// ForgeSettings returns this repository's normalized forge authority.
func (r Repository) ForgeSettings() (RepositoryForgeSettings, error) {
	return ParseRepositoryForgeSettings(r.Settings)
}

func normalizeRepositoryForgeSettings(settings RepositoryForgeSettings) (RepositoryForgeSettings, error) {
	settings.Provider = ForgeProvider(strings.ToLower(strings.TrimSpace(string(settings.Provider))))
	settings.BaseURL = strings.TrimSpace(settings.BaseURL)
	if settings.Provider == "" {
		settings.Provider = ForgeProviderGitHub
	}

	switch settings.Provider {
	case ForgeProviderGitHub:
		if settings.BaseURL == "" {
			return settings, nil
		}
		base := strings.TrimRight(settings.BaseURL, "/")
		if base != "https://github.com" {
			return RepositoryForgeSettings{}, fmt.Errorf("custom GitHub base_url is not supported by the current GitHub forge adapter")
		}
		settings.BaseURL = ""
		return settings, nil

	case ForgeProviderGitea:
		if settings.BaseURL == "" {
			return RepositoryForgeSettings{}, fmt.Errorf("gitea forge base_url is required")
		}
		parsed, err := url.Parse(settings.BaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return RepositoryForgeSettings{}, fmt.Errorf("gitea forge base_url must be an absolute URL")
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return RepositoryForgeSettings{}, fmt.Errorf("gitea forge base_url must use http or https")
		}
		if parsed.User != nil {
			return RepositoryForgeSettings{}, fmt.Errorf("gitea forge base_url must not contain userinfo")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return RepositoryForgeSettings{}, fmt.Errorf("gitea forge base_url must not contain query or fragment components")
		}
		normalizedPath := strings.TrimRight(parsed.Path, "/")
		if normalizedPath == "/api/v1" || strings.HasSuffix(normalizedPath, "/api/v1") {
			return RepositoryForgeSettings{}, fmt.Errorf("gitea forge base_url must be the instance root, not the /api/v1 API endpoint")
		}
		settings.BaseURL = strings.TrimRight(settings.BaseURL, "/")
		return settings, nil

	default:
		return RepositoryForgeSettings{}, fmt.Errorf("unsupported forge provider %q", settings.Provider)
	}
}
