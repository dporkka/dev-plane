package gateway

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RepositoryTokenSource resolves a short-lived credential for exactly one
// GitHub repository and only the permissions required by an operation.
type RepositoryTokenSource interface {
	Token(ctx context.Context, owner, repo string, permissions map[string]string) (string, error)
}

type GitHubAppTokenSource struct {
	appID      string
	privateKey *rsa.PrivateKey
	apiBaseURL string
	httpClient *http.Client
	now        func() time.Time
}

var githubRepoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var githubOwnerPart = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// NewGitHubAppTokenSource parses a GitHub App RSA signing key. The key
// remains process-private; the resulting installation tokens are never
// cached in global state, written to disk, or returned to API callers.
func NewGitHubAppTokenSource(appID, keyPEM string) (*GitHubAppTokenSource, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(appID), 10, 64)
	if err != nil || id <= 0 {
		return nil, errors.New("valid numeric GITHUB_APP_ID required")
	}
	keyPEM = strings.ReplaceAll(strings.TrimSpace(keyPEM), `\n`, "\n")
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errors.New("valid GITHUB_APP_PRIVATE_KEY PEM required")
	}
	var key *rsa.PrivateKey
	if block.Type == "RSA PRIVATE KEY" {
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	} else {
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		key, _ = parsed.(*rsa.PrivateKey)
	}
	if err != nil || key == nil {
		return nil, errors.New("GitHub App private key must be RSA PKCS1 or PKCS8")
	}
	return &GitHubAppTokenSource{
		appID: strconv.FormatInt(id, 10), privateKey: key, apiBaseURL: "https://api.github.com",
		httpClient: &http.Client{Timeout: 10 * time.Second}, now: time.Now,
	}, nil
}

// GitHubAppTokenSourceFromEnv is fail-closed: an incomplete config cannot
// silently fall back to a process-wide PAT.
func GitHubAppTokenSourceFromEnv() (*GitHubAppTokenSource, error) {
	id := os.Getenv("GITHUB_APP_ID")
	key := os.Getenv("GITHUB_APP_PRIVATE_KEY")
	if key == "" {
		if path := os.Getenv("GITHUB_APP_PRIVATE_KEY_FILE"); path != "" {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read GitHub App key file: %w", err)
			}
			key = string(raw)
		}
	}
	return NewGitHubAppTokenSource(id, key)
}

func (p *GitHubAppTokenSource) signedAppJWT() (string, error) {
	now := p.now().UTC()
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iss": p.appID, "iat": now.Add(-60 * time.Second).Unix(), "exp": now.Add(9 * time.Minute).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := head + "." + payload
	sum := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign GitHub App request: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (p *GitHubAppTokenSource) request(ctx context.Context, method, path, appJWT string, payload []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, p.apiBaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("construct GitHub App request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+appJWT)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	client := *p.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub App request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Never echo remote bodies: GitHub/other proxies can include credentials.
		return fmt.Errorf("GitHub App request rejected (HTTP %d)", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode GitHub App response: %w", err)
	}
	return nil
}

// Token generates a repository-restricted GitHub App installation token.
// Requests never ask for unrestricted installation access.
func (p *GitHubAppTokenSource) Token(ctx context.Context, owner, repo string, permissions map[string]string) (string, error) {
	if p == nil {
		return "", errors.New("GitHub App token provider missing")
	}
	if !githubOwnerPart.MatchString(owner) || !githubRepoPart.MatchString(repo) || repo == "." || repo == ".." {
		return "", errors.New("invalid GitHub repository identity")
	}
	if len(permissions) == 0 {
		return "", errors.New("explicit GitHub App permission scope required")
	}
	for name, level := range permissions {
		switch name {
		case "contents", "pull_requests", "deployments":
		default:
			return "", fmt.Errorf("unsupported GitHub App permission: %s", name)
		}
		if level != "read" && level != "write" {
			return "", errors.New("GitHub App permission must be read or write")
		}
	}
	jwt, err := p.signedAppJWT()
	if err != nil {
		return "", err
	}
	var installation struct {
		ID          int64   `json:"id"`
		SuspendedAt *string `json:"suspended_at"`
	}
	if err := p.request(ctx, http.MethodGet, "/repos/"+owner+"/"+repo+"/installation", jwt, nil, &installation); err != nil {
		return "", err
	}
	if installation.ID <= 0 || installation.SuspendedAt != nil {
		return "", errors.New("GitHub App installation unavailable")
	}
	reqBody, err := json.Marshal(map[string]any{"repositories": []string{repo}, "permissions": permissions})
	if err != nil {
		return "", err
	}
	var grant struct {
		Token        string    `json:"token"`
		ExpiresAt    time.Time `json:"expires_at"`
		Repositories []struct {
			FullName string `json:"full_name"`
		} `json:"repositories"`
		Permissions map[string]string `json:"permissions"`
	}
	path := "/app/installations/" + strconv.FormatInt(installation.ID, 10) + "/access_tokens"
	if err := p.request(ctx, http.MethodPost, path, jwt, reqBody, &grant); err != nil {
		return "", err
	}
	if grant.Token == "" || !grant.ExpiresAt.After(p.now().Add(time.Minute)) {
		return "", errors.New("GitHub App token missing or already expiring")
	}
	if len(grant.Repositories) != 1 || !strings.EqualFold(grant.Repositories[0].FullName, owner+"/"+repo) {
		return "", errors.New("GitHub App response did not prove single-repository scope")
	}
	for k, v := range permissions {
		if grant.Permissions[k] != v {
			return "", errors.New("GitHub App response does not match requested permission")
		}
	}
	for k, v := range grant.Permissions {
		if k == "metadata" && v == "read" {
			continue
		}
		if permissions[k] != v {
			return "", errors.New("GitHub App response includes unexpected permissions")
		}
	}
	return grant.Token, nil
}
