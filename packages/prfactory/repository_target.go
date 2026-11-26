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
func (f *Factory) getRepositoryTarget(ctx context.Context, repositoryID, projectID string) (repositoryTarget, error) {
	if f.db == nil {
		return repositoryTarget{}, fmt.Errorf("database is not configured")
	}
	var owner, name string
	var settings sql.NullString
	if err := f.db.QueryRowContext(ctx, `
		SELECT owner, name, settings FROM repositories
		WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL
        AND EXISTS (SELECT 1 FROM projects p JOIN organizations o ON o.id=p.organization_id
            WHERE p.id=$2 AND p.deleted_at IS NULL AND o.deleted_at IS NULL)
    `, repositoryID, projectID).Scan(&owner, &name, &settings); err != nil {
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
	// Git can rewrite origin differently for fetch and push, and can push to
	// multiple configured pushurl values. Check the effective destinations,
	// not just the fetch URL, before exposing any push credential.
	for _, options := range [][]string{{"--all"}, {"--push", "--all"}} {
		args := append([]string{"-C", workspacePath, "remote", "get-url"}, options...)
		cmd := exec.CommandContext(ctx, "git", append(args, "origin")...)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("read workspace origin: %w", err)
		}
		origin := strings.TrimSuffix(string(out), "\n")
		if origin == "" || strings.TrimSpace(origin) != origin || strings.ContainsAny(origin, "\x00\r\n") {
			return fmt.Errorf("workspace origin must resolve to exactly one destination")
		}
		if err := f.validateWorkspaceRemote(origin, target); err != nil {
			return err
		}
	}
	return nil
}

func (f *Factory) validateWorkspaceRemote(origin string, target repositoryTarget) error {
	base, err := repositoryForgeBaseURL(target.Forge)
	if err != nil {
		return err
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("invalid repository forge authority")
	}
	wantPath := path.Join("/", baseURL.Path, target.Owner, target.Name+".git")
	credentialed := strings.TrimSpace(f.gitPushCredential.Username) != "" || strings.TrimSpace(f.gitPushCredential.Password) != ""

	// Git commonly stores SSH remotes in SCP syntax (git@host:owner/repo.git).
	if host, remotePath, ok := parseSCPLikeSSHRemote(origin); ok {
		if credentialed {
			return fmt.Errorf("workspace origin uses ssh but HTTPS Git credentials are configured")
		}
		if !strings.EqualFold(host, baseURL.Hostname()) {
			return fmt.Errorf("workspace origin authority mismatch")
		}
		wantSSHPath := path.Join("/", target.Owner, target.Name+".git")
		if path.Clean(remotePath) != wantSSHPath {
			return fmt.Errorf("workspace origin repository mismatch")
		}
		return nil
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("workspace origin is not an absolute HTTPS/SSH URL or SCP-style SSH remote")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(origin, "#") {
		return fmt.Errorf("workspace origin must not contain query or fragment")
	}
	if parsed.User != nil {
		if parsed.Scheme != "ssh" {
			return fmt.Errorf("workspace origin must not contain embedded HTTP credentials")
		}
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return fmt.Errorf("workspace origin must not contain embedded SSH password")
		}
	}

	switch parsed.Scheme {
	case "http", "https":
		if parsed.Scheme != baseURL.Scheme {
			return fmt.Errorf("workspace origin scheme mismatch")
		}
		if !strings.EqualFold(parsed.Host, baseURL.Host) {
			return fmt.Errorf("workspace origin authority mismatch")
		}
	case "ssh":
		if credentialed {
			return fmt.Errorf("workspace origin uses ssh but HTTPS Git credentials are configured")
		}
		if !strings.EqualFold(parsed.Hostname(), baseURL.Hostname()) {
			return fmt.Errorf("workspace origin authority mismatch")
		}
		// SSH can use a service port separate from the web/API authority.
		wantPath = path.Join("/", target.Owner, target.Name+".git")
	default:
		return fmt.Errorf("workspace origin uses unsupported scheme")
	}

	if path.Clean(parsed.Path) != wantPath {
		return fmt.Errorf("workspace origin repository mismatch")
	}
	return nil
}

func parseSCPLikeSSHRemote(raw string) (host, remotePath string, ok bool) {
	if strings.Contains(raw, "://") {
		return "", "", false
	}
	at := strings.LastIndex(raw, "@")
	if at <= 0 || at == len(raw)-1 {
		return "", "", false
	}
	rest := raw[at+1:]
	colon := strings.Index(rest, ":")
	if colon <= 0 || colon == len(rest)-1 {
		return "", "", false
	}
	host = rest[:colon]
	remote := rest[colon+1:]
	if strings.ContainsAny(host, "/\\") || strings.ContainsAny(remote, "\x00\r\n") {
		return "", "", false
	}
	return host, "/" + strings.TrimPrefix(remote, "/"), true
}
