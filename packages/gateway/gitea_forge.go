package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GiteaForge implements Forge against Gitea's v1 API.
type GiteaForge struct {
	apiBaseURL string
	httpClient *http.Client
}

var _ Forge = (*GiteaForge)(nil)

// NewGiteaForge creates a provider for a Gitea instance URL. instanceURL may
// include a deployment subpath; /api/v1 is appended unless it is already present.
func NewGiteaForge(instanceURL string, client *http.Client) (*GiteaForge, error) {
	instanceURL = strings.TrimSpace(instanceURL)
	parsed, err := url.Parse(instanceURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid gitea instance URL %q", instanceURL)
	}

	base := strings.TrimRight(instanceURL, "/")
	if !strings.HasSuffix(base, "/api/v1") {
		base += "/api/v1"
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &GiteaForge{apiBaseURL: base, httpClient: client}, nil
}

func (f *GiteaForge) Name() string { return "gitea" }

func (f *GiteaForge) ListRepositories(ctx context.Context, credential ForgeCredential, page int) ([]ForgeRepository, error) {
	if page < 1 {
		page = 1
	}
	endpoint := fmt.Sprintf("%s/user/repos?page=%d&limit=100", f.apiBaseURL, page)
	var repos []giteaRepository
	if err := f.do(ctx, credential, http.MethodGet, endpoint, nil, &repos); err != nil {
		return nil, fmt.Errorf("list gitea repositories: %w", err)
	}
	out := make([]ForgeRepository, 0, len(repos))
	for _, repo := range repos {
		out = append(out, repo.normalize())
	}
	return out, nil
}

func (f *GiteaForge) GetRepository(ctx context.Context, credential ForgeCredential, owner, name string) (*ForgeRepository, error) {
	endpoint := f.repoURL(owner, name)
	var repo giteaRepository
	if err := f.do(ctx, credential, http.MethodGet, endpoint, nil, &repo); err != nil {
		return nil, fmt.Errorf("get gitea repository %s/%s: %w", owner, name, err)
	}
	normalized := repo.normalize()
	return &normalized, nil
}

func (f *GiteaForge) CreatePullRequest(ctx context.Context, credential ForgeCredential, owner, name string, pr ForgeNewPullRequest) (*ForgePullRequest, error) {
	if pr.Draft {
		return nil, fmt.Errorf("%w: gitea v1 create-pull-request does not expose draft creation", ErrUnsupportedForgeCapability)
	}
	payload := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  string `json:"head"`
		Base  string `json:"base"`
	}{
		Title: pr.Title,
		Body:  pr.Body,
		Head:  pr.Head,
		Base:  pr.Base,
	}

	var created giteaPullRequest
	if err := f.do(ctx, credential, http.MethodPost, f.repoURL(owner, name)+"/pulls", payload, &created); err != nil {
		return nil, fmt.Errorf("create gitea pull request: %w", err)
	}
	normalized := created.normalize()
	return &normalized, nil
}

func (f *GiteaForge) MergePullRequest(ctx context.Context, credential ForgeCredential, owner, name string, number int, req ForgeMergeRequest) (*ForgeMergeResult, error) {
	method := req.Method
	if method == "" {
		method = "merge"
	}
	switch method {
	case "merge", "squash", "rebase":
	default:
		return nil, fmt.Errorf("unsupported merge method %q", method)
	}

	payload := map[string]any{"do": method}
	if req.ExpectedHeadSHA != "" {
		payload["head_commit_id"] = req.ExpectedHeadSHA
	}
	if req.Title != "" {
		payload["merge_title_field"] = req.Title
	}
	if req.Message != "" {
		payload["merge_message_field"] = req.Message
	}

	endpoint := f.repoURL(owner, name) + "/pulls/" + strconv.Itoa(number) + "/merge"
	if err := f.do(ctx, credential, http.MethodPost, endpoint, payload, nil); err != nil {
		return nil, fmt.Errorf("merge gitea pull request: %w", err)
	}
	return &ForgeMergeResult{Merged: true}, nil
}

func (f *GiteaForge) CreateWebhook(ctx context.Context, credential ForgeCredential, owner, name, callbackURL, secret string) (int64, error) {
	payload := map[string]any{
		"type":   "gitea",
		"active": true,
		"events": []string{"push", "pull_request", "issues"},
		"config": map[string]string{
			"url":          callbackURL,
			"content_type": "json",
			"secret":       secret,
		},
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := f.do(ctx, credential, http.MethodPost, f.repoURL(owner, name)+"/hooks", payload, &created); err != nil {
		return 0, fmt.Errorf("create gitea webhook: %w", err)
	}
	return created.ID, nil
}

func (f *GiteaForge) DeleteWebhook(ctx context.Context, credential ForgeCredential, owner, name string, hookID int64) error {
	endpoint := f.repoURL(owner, name) + "/hooks/" + strconv.FormatInt(hookID, 10)
	if err := f.do(ctx, credential, http.MethodDelete, endpoint, nil, nil); err != nil {
		return fmt.Errorf("delete gitea webhook: %w", err)
	}
	return nil
}

func (f *GiteaForge) repoURL(owner, name string) string {
	return fmt.Sprintf("%s/repos/%s/%s", f.apiBaseURL, url.PathEscape(owner), url.PathEscape(name))
}

func (f *GiteaForge) do(ctx context.Context, credential ForgeCredential, method, endpoint string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal gitea request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if credential.AccessToken != "" {
		req.Header.Set("Authorization", "token "+credential.AccessToken)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gitea API error %d: %s", resp.StatusCode, strings.TrimSpace(string(errorBody)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode gitea response: %w", err)
	}
	return nil
}

type giteaRepository struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Description   string    `json:"description"`
	Private       bool      `json:"private"`
	CloneURL      string    `json:"clone_url"`
	SSHURL        string    `json:"ssh_url"`
	HTMLURL       string    `json:"html_url"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"updated_at"`
}

func (r giteaRepository) normalize() ForgeRepository {
	return ForgeRepository{
		ID:            r.ID,
		Name:          r.Name,
		FullName:      r.FullName,
		Description:   r.Description,
		Private:       r.Private,
		CloneURL:      r.CloneURL,
		SSHURL:        r.SSHURL,
		HTMLURL:       r.HTMLURL,
		DefaultBranch: r.DefaultBranch,
		PushedAt:      r.PushedAt,
	}
}

type giteaPullRequest struct {
	ID      int64  `json:"id"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (pr giteaPullRequest) normalize() ForgePullRequest {
	return ForgePullRequest{
		ID:        pr.ID,
		Number:    pr.Number,
		Title:     pr.Title,
		Body:      pr.Body,
		State:     pr.State,
		HTMLURL:   pr.HTMLURL,
		Draft:     pr.Draft,
		Head:      ForgeBranchRef{Ref: pr.Head.Ref, SHA: pr.Head.SHA},
		Base:      ForgeBranchRef{Ref: pr.Base.Ref, SHA: pr.Base.SHA},
		CreatedAt: pr.CreatedAt,
		UpdatedAt: pr.UpdatedAt,
	}
}
