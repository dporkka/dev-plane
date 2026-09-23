package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GiteaGateway implements the agent forge contract against a Gitea instance.
type GiteaGateway struct {
	baseURL    string
	apiBaseURL string
	httpClient *http.Client
}

type GiteaRepo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
}

type GiteaPR struct {
	ID      int64  `json:"id"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
}

func NewGiteaGateway(baseURL string) *GiteaGateway {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://gitea.com"
	}
	return &GiteaGateway{
		baseURL:    baseURL,
		apiBaseURL: baseURL + "/api/v1",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (g *GiteaGateway) GetRepo(ctx context.Context, token, owner, name string) (*GiteaRepo, error) {
	var repo GiteaRepo
	if err := g.do(ctx, http.MethodGet, g.repoPath(owner, name), token, nil, &repo); err != nil {
		return nil, fmt.Errorf("get gitea repo %s/%s: %w", owner, name, err)
	}
	return &repo, nil
}

func (g *GiteaGateway) CreateWebhook(ctx context.Context, token, owner, name, callbackURL, secret string) (int64, error) {
	payload := map[string]any{
		"type":   "gitea",
		"active": true,
		"events": []string{"push", "pull_request", "pull_request_review"},
		"config": map[string]string{
			"url":          callbackURL,
			"content_type": "json",
			"secret":       secret,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal gitea webhook payload: %w", err)
	}
	var result struct {
		ID int64 `json:"id"`
	}
	if err := g.do(ctx, http.MethodPost, g.repoPath(owner, name)+"/hooks", token, body, &result); err != nil {
		return 0, fmt.Errorf("create gitea webhook: %w", err)
	}
	return result.ID, nil
}

func (g *GiteaGateway) CreatePullRequest(ctx context.Context, token, owner, name string, pr NewPR) (*ForgePullRequest, error) {
	title := pr.Title
	if pr.Draft && !isWIPTitle(title) {
		title = "WIP: " + title
	}
	payload := map[string]any{
		"title": title,
		"body":  pr.Body,
		"head":  pr.Head,
		"base":  pr.Base,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal gitea pr payload: %w", err)
	}
	var result GiteaPR
	if err := g.do(ctx, http.MethodPost, g.repoPath(owner, name)+"/pulls", token, body, &result); err != nil {
		return nil, fmt.Errorf("create gitea pr: %w", err)
	}
	return &ForgePullRequest{
		Number: result.Number, HTMLURL: result.HTMLURL, State: result.State, Draft: result.Draft || pr.Draft,
	}, nil
}

func (g *GiteaGateway) MergePullRequest(ctx context.Context, token, owner, name string, number int, req MergePRRequest) (*ForgeMergeResult, error) {
	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = "merge"
	}
	switch method {
	case "merge", "squash", "rebase":
	default:
		return nil, fmt.Errorf("unsupported gitea merge method %q", method)
	}
	payload := map[string]any{"do": method}
	if req.Title != "" {
		payload["merge_title_field"] = req.Title
	}
	if req.Message != "" {
		payload["merge_message_field"] = req.Message
	}
	if req.SHA != "" {
		payload["head_commit_id"] = req.SHA
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal gitea merge payload: %w", err)
	}
	if err := g.do(ctx, http.MethodPost, fmt.Sprintf("%s/pulls/%d/merge", g.repoPath(owner, name), number), token, body, nil); err != nil {
		return nil, fmt.Errorf("merge gitea pr: %w", err)
	}
	return &ForgeMergeResult{SHA: req.SHA, Merged: true, Message: "merged"}, nil
}

func (g *GiteaGateway) repoPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func (g *GiteaGateway) do(ctx context.Context, method, path, token string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.apiBaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token = strings.TrimSpace(token); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gitea API error %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func isWIPTitle(title string) bool {
	value := strings.ToLower(strings.TrimSpace(title))
	return strings.HasPrefix(value, "wip:") || strings.HasPrefix(value, "[wip]")
}
