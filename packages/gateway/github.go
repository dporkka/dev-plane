package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/oauth2"
)

// GitHubGateway provides methods for interacting with the GitHub API.
type GitHubGateway struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client
	oauthConfig  *oauth2.Config
	apiBaseURL   string
}

// GitHubUser represents a GitHub user profile.
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	Type      string `json:"type"`
}

// GitHubRepo represents a GitHub repository.
type GitHubRepo struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	FullName    string    `json:"full_name"`
	Description string    `json:"description"`
	Private     bool      `json:"private"`
	CloneURL    string    `json:"clone_url"`
	SSHURL      string    `json:"ssh_url"`
	HTMLURL     string    `json:"html_url"`
	DefaultBranch string  `json:"default_branch"`
	Language    string    `json:"language"`
	Stargazers  int       `json:"stargazers_count"`
	PushedAt    time.Time `json:"pushed_at"`
	Permissions struct {
		Admin bool `json:"admin"`
		Push  bool `json:"push"`
		Pull  bool `json:"pull"`
	} `json:"permissions"`
}

// GitHubPR represents a GitHub pull request.
type GitHubPR struct {
	ID        int64     `json:"id"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	Head      struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	User      GitHubUser `json:"user"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// NewPR represents the parameters for creating a new pull request.
type NewPR struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"` // branch name
	Base  string `json:"base"` // target branch
	Draft bool   `json:"draft"`
}

// MergePRRequest represents the parameters for merging a pull request.
type MergePRRequest struct {
	// Method is the merge method: "merge", "squash", or "rebase".
	// Defaults to "merge" when empty.
	Method string `json:"merge_method,omitempty"`
	// Title is the title of the merge commit (optional).
	Title string `json:"commit_title,omitempty"`
	// Message is the message of the merge commit (optional).
	Message string `json:"commit_message,omitempty"`
	// SHA is the expected HEAD SHA of the pull request. When provided, the
	// merge fails if the pull request HEAD does not match.
	SHA string `json:"sha,omitempty"`
}

// MergePRResult is the response from a successful GitHub merge call.
type MergePRResult struct {
	SHA     string `json:"sha"`
	Merged  bool   `json:"merged"`
	Message string `json:"message"`
}

// Deployment represents a GitHub deployment.
type Deployment struct {
	ID        int64  `json:"id"`
	NodeID    string `json:"node_id"`
	URL       string `json:"url"`
	StatusURL string `json:"statuses_url"`
	Ref       string `json:"ref"`
	Task      string `json:"task"`
	State     string `json:"state"`
}

// DeploymentStatus represents a GitHub deployment status.
type DeploymentStatus struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
	URL   string `json:"url"`
}

// NewGitHubGateway creates a new GitHub gateway with the given OAuth credentials.
func NewGitHubGateway(clientID, clientSecret string) *GitHubGateway {
	oauthConfig := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   "https://github.com/login/oauth/authorize",
			TokenURL:  "https://github.com/login/oauth/access_token",
		},
		Scopes: []string{"repo", "read:org", "user:email"},
	}

	return &GitHubGateway{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		oauthConfig:  oauthConfig,
		apiBaseURL:   "https://api.github.com",
	}
}

// SetRedirectURL configures the OAuth redirect URL.
func (g *GitHubGateway) SetRedirectURL(url string) {
	g.oauthConfig.RedirectURL = url
}

// AuthURL returns the GitHub OAuth authorization URL.
func (g *GitHubGateway) AuthURL(state string) string {
	return g.oauthConfig.AuthCodeURL(state)
}

// ExchangeCode exchanges an OAuth authorization code for an access token.
func (g *GitHubGateway) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	token, err := g.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange oauth code: %w", err)
	}
	return token, nil
}

// GetUser retrieves the authenticated user's GitHub profile.
func (g *GitHubGateway) GetUser(ctx context.Context, token *oauth2.Token) (*GitHubUser, error) {
	var user GitHubUser
	if err := g.get(ctx, token, g.apiBaseURL+"/user", &user); err != nil {
		return nil, fmt.Errorf("get github user: %w", err)
	}
	return &user, nil
}

// ListRepos lists repositories accessible to the authenticated user.
func (g *GitHubGateway) ListRepos(ctx context.Context, token *oauth2.Token, page int) ([]GitHubRepo, error) {
	if page < 1 {
		page = 1
	}
	url := fmt.Sprintf("%s/user/repos?sort=updated&per_page=100&page=%d", g.apiBaseURL, page)
	var repos []GitHubRepo
	if err := g.get(ctx, token, url, &repos); err != nil {
		return nil, fmt.Errorf("list github repos: %w", err)
	}
	return repos, nil
}

// GetRepo retrieves a specific repository.
func (g *GitHubGateway) GetRepo(ctx context.Context, token *oauth2.Token, owner, name string) (*GitHubRepo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s", g.apiBaseURL, owner, name)
	var repo GitHubRepo
	if err := g.get(ctx, token, url, &repo); err != nil {
		return nil, fmt.Errorf("get github repo %s/%s: %w", owner, name, err)
	}
	return &repo, nil
}

// CreateWebhook creates a repository webhook for receiving events.
func (g *GitHubGateway) CreateWebhook(ctx context.Context, token *oauth2.Token, owner, name, callbackURL, secret string) (int64, error) {
	payload := map[string]any{
		"name":   "web",
		"active": true,
		"events": []string{"push", "pull_request", "issues"},
		"config": map[string]string{
			"url":          callbackURL,
			"content_type": "json",
			"secret":       secret,
		},
	}

	url := fmt.Sprintf("%s/repos/%s/%s/hooks", g.apiBaseURL, owner, name)
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal webhook payload: %w", err)
	}

	var result struct {
		ID int64 `json:"id"`
	}
	if err := g.post(ctx, token, url, body, &result); err != nil {
		return 0, fmt.Errorf("create github webhook: %w", err)
	}
	return result.ID, nil
}

// CreatePR creates a new pull request.
func (g *GitHubGateway) CreatePR(ctx context.Context, token *oauth2.Token, owner, name string, pr NewPR) (*GitHubPR, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls", g.apiBaseURL, owner, name)
	body, err := json.Marshal(pr)
	if err != nil {
		return nil, fmt.Errorf("marshal pr payload: %w", err)
	}

	var result GitHubPR
	if err := g.post(ctx, token, url, body, &result); err != nil {
		return nil, fmt.Errorf("create github pr: %w", err)
	}
	return &result, nil
}

// MergePR merges a pull request on GitHub.
func (g *GitHubGateway) MergePR(ctx context.Context, token *oauth2.Token, owner, name string, number int, req MergePRRequest) (*MergePRResult, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/merge", g.apiBaseURL, owner, name, number)

	if req.Method == "" {
		req.Method = "merge"
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal merge payload: %w", err)
	}

	var result MergePRResult
	if err := g.put(ctx, token, url, body, &result); err != nil {
		return nil, fmt.Errorf("merge github pr: %w", err)
	}
	return &result, nil
}

// CreateDeployment creates a new GitHub deployment for the given ref and environment.
func (g *GitHubGateway) CreateDeployment(ctx context.Context, token *oauth2.Token, owner, name, environment, ref string) (*Deployment, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/deployments", g.apiBaseURL, owner, name)
	payload := map[string]any{
		"ref":         ref,
		"environment": environment,
		"auto_merge":  false,
		"required_contexts": []string{},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal deployment payload: %w", err)
	}

	var result Deployment
	if err := g.post(ctx, token, url, body, &result); err != nil {
		return nil, fmt.Errorf("create github deployment: %w", err)
	}
	return &result, nil
}

// CreateDeploymentStatus creates a status update for a GitHub deployment.
func (g *GitHubGateway) CreateDeploymentStatus(ctx context.Context, token *oauth2.Token, owner, name string, deploymentID int64, state, targetURL string) (*DeploymentStatus, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/deployments/%d/statuses", g.apiBaseURL, owner, name, deploymentID)
	payload := map[string]any{
		"state": state,
	}
	if targetURL != "" {
		payload["target_url"] = targetURL
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal deployment status payload: %w", err)
	}

	var result DeploymentStatus
	if err := g.post(ctx, token, url, body, &result); err != nil {
		return nil, fmt.Errorf("create github deployment status: %w", err)
	}
	return &result, nil
}

// DeleteWebhook removes a webhook from a repository.
func (g *GitHubGateway) DeleteWebhook(ctx context.Context, token *oauth2.Token, owner, name string, hookID int64) error {
	url := fmt.Sprintf("%s/repos/%s/%s/hooks/%d", g.apiBaseURL, owner, name, hookID)
	return g.del(ctx, token, url)
}

// get performs an authenticated GET request and decodes the JSON response.
func (g *GitHubGateway) get(ctx context.Context, token *oauth2.Token, url string, out any) error {
	client := g.oauthConfig.Client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github API error %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// post performs an authenticated POST request and decodes the JSON response.
func (g *GitHubGateway) post(ctx context.Context, token *oauth2.Token, url string, body []byte, out any) error {
	client := g.oauthConfig.Client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github API error %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// put performs an authenticated PUT request and decodes the JSON response.
func (g *GitHubGateway) put(ctx context.Context, token *oauth2.Token, url string, body []byte, out any) error {
	client := g.oauthConfig.Client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github API error %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// del performs an authenticated DELETE request.
func (g *GitHubGateway) del(ctx context.Context, token *oauth2.Token, url string) error {
	client := g.oauthConfig.Client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github API error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ParseInstallationID converts a GitHub app installation ID string to int64.
func ParseInstallationID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
