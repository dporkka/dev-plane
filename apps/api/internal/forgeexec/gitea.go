package forgeexec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type GiteaExecutor struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewGiteaExecutor(baseURL, token string, client *http.Client) *GiteaExecutor {
	if client == nil {
		client = http.DefaultClient
	}
	return &GiteaExecutor{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		client:  client,
	}
}

func (g *GiteaExecutor) Execute(ctx context.Context, command Command) (Response, error) {
	if g.baseURL == "" || g.token == "" {
		return Response{}, fmt.Errorf("Gitea executor is not configured")
	}
	if err := command.Repository.Validate(); err != nil {
		return Response{}, err
	}
	switch command.Type {
	case "read_file":
		return g.readFile(ctx, command)
	case "create_branch":
		return g.createBranch(ctx, command)
	case "write_file":
		return g.writeFile(ctx, command)
	case "create_change":
		return g.createChange(ctx, command)
	case "review_change":
		return g.reviewChange(ctx, command)
	case "merge_change":
		return g.mergeChange(ctx, command)
	case "list_checks":
		return g.listChecks(ctx, command)
	default:
		return Response{}, fmt.Errorf("unknown forge command type %q", command.Type)
	}
}

func (g *GiteaExecutor) readFile(ctx context.Context, command Command) (Response, error) {
	if err := validateFilePath(command.Path); err != nil {
		return Response{}, err
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s",
		command.Repository.Owner, command.Repository.Name, encodePath(command.Path))
	query := url.Values{}
	if command.Reference != nil {
		query.Set("ref", *command.Reference)
	}
	var out struct {
		Path     string `json:"path"`
		SHA      string `json:"sha"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := g.doJSON(ctx, http.MethodGet, path, query, nil, &out); err != nil {
		return Response{}, err
	}
	content := []byte(out.Content)
	if out.Encoding == "" || strings.EqualFold(out.Encoding, "base64") {
		decoded, err := base64.StdEncoding.DecodeString(stripWhitespace(out.Content))
		if err != nil {
			return Response{}, fmt.Errorf("decode Gitea file content: %w", err)
		}
		content = decoded
	}
	if out.Path == "" {
		out.Path = command.Path
	}
	if out.SHA == "" {
		return Response{}, fmt.Errorf("Gitea file response missing sha")
	}
	return Response{Type: "file", Value: FileContent{
		Path: out.Path, Content: intsFromBytes(content), SHA: out.SHA,
	}}, nil
}

func (g *GiteaExecutor) createBranch(ctx context.Context, command Command) (Response, error) {
	if err := requireText("branch name", command.Name); err != nil {
		return Response{}, err
	}
	if err := requireText("branch source", command.From); err != nil {
		return Response{}, err
	}
	var out struct {
		Name   string `json:"name"`
		Commit struct {
			ID  string `json:"id"`
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	err := g.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/repos/%s/%s/branches", command.Repository.Owner, command.Repository.Name),
		nil,
		map[string]any{"new_branch_name": command.Name, "old_ref_name": command.From},
		&out,
	)
	if err != nil {
		return Response{}, err
	}
	commitID := out.Commit.ID
	if commitID == "" {
		commitID = out.Commit.SHA
	}
	if out.Name == "" || commitID == "" {
		return Response{}, fmt.Errorf("Gitea branch response missing name or commit id")
	}
	return Response{Type: "branch", Value: BranchRef{Name: out.Name, CommitID: commitID}}, nil
}

func (g *GiteaExecutor) writeFile(ctx context.Context, command Command) (Response, error) {
	if err := validateFilePath(command.Path); err != nil {
		return Response{}, err
	}
	if err := requireText("branch", command.Branch); err != nil {
		return Response{}, err
	}
	if err := requireText("commit message", command.Message); err != nil {
		return Response{}, err
	}
	content, err := bytesFromInts(command.Content)
	if err != nil {
		return Response{}, err
	}
	method := http.MethodPost
	payload := map[string]any{
		"branch":  command.Branch,
		"content": base64.StdEncoding.EncodeToString(content),
		"message": command.Message,
	}
	if command.ExpectedBlobSHA != nil {
		method = http.MethodPut
		payload["sha"] = *command.ExpectedBlobSHA
	}
	var out struct {
		Commit struct {
			SHA string `json:"sha"`
			ID  string `json:"id"`
		} `json:"commit"`
	}
	if err := g.doJSON(ctx, method,
		fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s", command.Repository.Owner, command.Repository.Name, encodePath(command.Path)),
		nil, payload, &out); err != nil {
		return Response{}, err
	}
	id := out.Commit.SHA
	if id == "" {
		id = out.Commit.ID
	}
	if id == "" {
		return Response{}, fmt.Errorf("Gitea commit response missing id")
	}
	return Response{Type: "commit", Value: CommitRef{ID: id}}, nil
}

func (g *GiteaExecutor) createChange(ctx context.Context, command Command) (Response, error) {
	if err := requireText("pull request head", command.Head); err != nil {
		return Response{}, err
	}
	if err := requireText("pull request base", command.Base); err != nil {
		return Response{}, err
	}
	if err := requireText("pull request title", command.Title); err != nil {
		return Response{}, err
	}
	var out struct {
		Number  uint64                      `json:"number"`
		HTMLURL string                      `json:"html_url"`
		URL     string                      `json:"url"`
		State   string                      `json:"state"`
		Head    struct{ Ref, Label string } `json:"head"`
		Base    struct{ Ref, Label string } `json:"base"`
	}
	if err := g.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/repos/%s/%s/pulls", command.Repository.Owner, command.Repository.Name),
		nil,
		map[string]any{"head": command.Head, "base": command.Base, "title": command.Title, "body": command.Body},
		&out); err != nil {
		return Response{}, err
	}
	head, base := out.Head.Ref, out.Base.Ref
	if head == "" {
		head = out.Head.Label
	}
	if base == "" {
		base = out.Base.Label
	}
	rawURL := out.HTMLURL
	if rawURL == "" {
		rawURL = out.URL
	}
	var resultURL *string
	if rawURL != "" {
		resultURL = &rawURL
	}
	return Response{Type: "change", Value: ChangeRef{
		Number: out.Number, URL: resultURL, Head: head, Base: base, State: out.State,
	}}, nil
}

func (g *GiteaExecutor) reviewChange(ctx context.Context, command Command) (Response, error) {
	event := map[string]string{
		"approve":         "APPROVED",
		"request_changes": "REQUEST_CHANGES",
		"comment":         "COMMENT",
	}[command.Event]
	if event == "" {
		return Response{}, fmt.Errorf("invalid review event %q", command.Event)
	}
	payload := map[string]any{"body": command.Body, "event": event}
	if command.CommitID != nil {
		payload["commit_id"] = *command.CommitID
	}
	var out struct {
		ID *uint64 `json:"id"`
	}
	if err := g.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/reviews", command.Repository.Owner, command.Repository.Name, command.Number),
		nil, payload, &out); err != nil {
		return Response{}, err
	}
	return Response{Type: "review", Value: ReviewRef{ID: out.ID}}, nil
}

func (g *GiteaExecutor) mergeChange(ctx context.Context, command Command) (Response, error) {
	method := map[string]string{
		"merge":             "merge",
		"rebase":            "rebase",
		"rebase_merge":      "rebase-merge",
		"squash":            "squash",
		"fast_forward_only": "fast-forward-only",
	}[command.Method]
	if method == "" {
		return Response{}, fmt.Errorf("invalid merge method %q", command.Method)
	}
	payload := map[string]any{"do": method}
	if command.ExpectedHeadSHA != nil {
		payload["head_commit_id"] = *command.ExpectedHeadSHA
	}
	if err := g.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/merge", command.Repository.Owner, command.Repository.Name, command.Number),
		nil, payload, nil); err != nil {
		return Response{}, err
	}
	return Response{Type: "merge", Value: MergeResult{Merged: true}}, nil
}

func (g *GiteaExecutor) listChecks(ctx context.Context, command Command) (Response, error) {
	if command.Reference == nil {
		return Response{}, fmt.Errorf("check reference is required")
	}
	if err := requireText("check reference", *command.Reference); err != nil {
		return Response{}, err
	}
	var items []struct {
		ID        json.RawMessage `json:"id"`
		Context   string          `json:"context"`
		Status    string          `json:"status"`
		State     string          `json:"state"`
		TargetURL *string         `json:"target_url"`
	}
	if err := g.doJSON(ctx, http.MethodGet,
		fmt.Sprintf("/api/v1/repos/%s/%s/commits/%s/statuses",
			command.Repository.Owner, command.Repository.Name, url.PathEscape(*command.Reference)),
		nil, nil, &items); err != nil {
		return Response{}, err
	}
	checks := make([]CheckRun, 0, len(items))
	for _, item := range items {
		id, err := rawID(item.ID)
		if err != nil {
			return Response{}, err
		}
		state := item.Status
		if state == "" {
			state = item.State
		}
		checks = append(checks, CheckRun{ID: id, Context: item.Context, State: state, TargetURL: item.TargetURL})
	}
	return Response{Type: "checks", Value: checks}, nil
}

func (g *GiteaExecutor) doJSON(ctx context.Context, method, path string, query url.Values, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal Gitea request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	target := g.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("create Gitea request: %w", err)
	}
	req.Header.Set("Authorization", "token "+g.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("Gitea transport: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read Gitea response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Gitea returned HTTP %d: %s", resp.StatusCode, truncate(string(responseBody), 512))
	}
	if out == nil || len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("decode Gitea response: %w", err)
	}
	return nil
}

func encodePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func stripWhitespace(value string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, value)
}

func rawID(value json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return text, nil
	}
	var number uint64
	if err := json.Unmarshal(value, &number); err == nil {
		return strconv.FormatUint(number, 10), nil
	}
	return "", fmt.Errorf("Gitea status response missing id")
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
