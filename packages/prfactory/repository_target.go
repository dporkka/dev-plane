package prfactory

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"strings"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

type repositoryTarget struct {
	Owner string
	Name  string
	Forge models.RepositoryForgeSettings
}

// getRepositoryTarget loads the repository-owned forge authority. Existing rows
// without a forge settings block resolve to GitHub for backward compatibility.
func (f *Factory) getRepositoryTarget(ctx context.Context, repositoryID string) (repositoryTarget, error) {
	if f.db == nil {
		return repositoryTarget{}, fmt.Errorf("database is not configured")
	}
	var owner, name string
	var settings sql.NullString
	if err := f.db.QueryRowContext(ctx, `
		SELECT owner, name, settings FROM repositories
		WHERE id = $1 AND deleted_at IS NULL
	`, repositoryID).Scan(&owner, &name, &settings); err != nil {
		return repositoryTarget{}, fmt.Errorf("get repository target: %w", err)
	}

	var raw []byte
	if settings.Valid {
		raw = []byte(settings.String)
	}
	forge, err := models.ParseRepositoryForgeSettings(raw)
	if err != nil {
		return repositoryTarget{}, fmt.Errorf("parse repository forge settings: %w", err)
	}
	return repositoryTarget{Owner: owner, Name: name, Forge: forge}, nil
}

// validateRepositoryForge binds process-level credentials to the exact persisted
// forge instance, not merely to a provider family such as "gitea".
func (f *Factory) validateRepositoryForge(target repositoryTarget) error {
	if f.forge == nil {
		return fmt.Errorf("forge is not configured")
	}
	configured := strings.ToLower(strings.TrimSpace(f.forge.Name()))
	required := strings.ToLower(strings.TrimSpace(string(target.Forge.Provider)))
	if configured != required {
		return fmt.Errorf("repository requires forge provider %s, configured forge is %s", required, configured)
	}

	authorityProvider, ok := f.forge.(gateway.ForgeAuthorityProvider)
	if !ok {
		return fmt.Errorf("configured forge %s does not expose credential authority", configured)
	}
	authority := authorityProvider.Authority()
	if strings.ToLower(strings.TrimSpace(authority.Provider)) != required {
		return fmt.Errorf("forge authority provider mismatch: repository requires %s, adapter authority is %s", required, authority.Provider)
	}

	expectedBase, err := repositoryForgeBaseURL(target.Forge)
	if err != nil {
		return err
	}
	actualBase, err := canonicalInstanceRoot(authority.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid configured forge authority: %w", err)
	}
	if expectedBase != actualBase {
		return fmt.Errorf("forge instance mismatch: repository requires %s, configured forge is %s", expectedBase, actualBase)
	}
	return nil
}

func repositoryForgeBaseURL(settings models.RepositoryForgeSettings) (string, error) {
	switch settings.Provider {
	case models.ForgeProviderGitHub:
		return "https://github.com", nil
	case models.ForgeProviderGitea:
		return canonicalInstanceRoot(settings.BaseURL)
	default:
		return "", fmt.Errorf("unsupported forge provider %q", settings.Provider)
	}
}

func canonicalInstanceRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("forge instance must be an absolute URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("forge instance must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("forge instance must not contain userinfo, query, or fragment")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

// validateWorkspaceOrigin verifies repository identity before push credentials are
// attached. This prevents an attacker-controlled workspace origin from receiving
// a token intended for another Gitea/GitHub instance.
func (f *Factory) validateWorkspaceOrigin(ctx context.Context, workspacePath string, target repositoryTarget) error {
	cmd := exec.CommandContext(ctx, "git", "-C", workspacePath, "remote", "get-url", "origin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("read workspace origin: %w: %s", err, strings.TrimSpace(string(out)))
	}
	origin := strings.TrimSpace(string(out))
	if origin == "" {
		return fmt.Errorf("workspace origin is empty")
	}

	base, err := repositoryForgeBaseURL(target.Forge)
	if err != nil {
		return err
	}
	baseURL, _ := url.Parse(base)
	wantPath := path.Join(baseURL.Path, target.Owner, target.Name+".git")

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("workspace origin %q is not an absolute HTTPS/SSH URL", origin)
	}
	if parsed.User != nil && parsed.Scheme != "ssh" {
		return fmt.Errorf("workspace origin must not contain embedded HTTP credentials")
	}
	if !strings.EqualFold(parsed.Hostname(), baseURL.Hostname()) {
		return fmt.Errorf("workspace origin authority mismatch: expected host %s, got %s", baseURL.Hostname(), parsed.Hostname())
	}

	remotePath := path.Clean(parsed.Path)
	if parsed.Scheme == "ssh" {
		wantPath = path.Join("/", target.Owner, target.Name+".git")
	}
	if remotePath != path.Clean(wantPath) {
		return fmt.Errorf("workspace origin repository mismatch: expected path %s, got %s", wantPath, remotePath)
	}

	credentialed := strings.TrimSpace(f.gitPushCredential.Username) != "" || strings.TrimSpace(f.gitPushCredential.Password) != ""
	if credentialed && parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("workspace origin uses %s but HTTPS Git credentials are configured", parsed.Scheme)
	}
	return nil
}
