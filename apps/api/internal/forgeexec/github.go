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

const githubAPIVersion = "2026-03-10"

type GitHubExecutor struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewGitHubExecutor(baseURL, token string, client *http.Client) *GitHubExecutor {
	if client == nil {
		client = http.DefaultClient
	}
	return &GitHubExecutor{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		client:  client,
	}
}

func (g *GitHubExecutor) Provider() string {
	return "github"
}

func (g *GitHubExecutor) Execute(ctx context.Context, command Command) (Response, error) {
	if err := g.validate(command); err != nil {
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

func (g *GitHubExecutor) Reconcile(ctx context.Context, command Command) (ReconcileResult, error) {
	if err := g.validate(command); err != nil {
		return ReconcileResult{}, err
	}
	switch command.Type {
	case "create_branch":
		return g.reconcileCreateBranch(ctx, command)
	case "write_file":
		return g.reconcileWriteFile(ctx, command)
	case "create_change":
		return g.reconcileCreateChange(ctx, command)
	case "review_change":
		return g.reconcileReviewChange(ctx, command)
	case "merge_change":
		return g.reconcileMergeChange(ctx, command)
	default:
		evidence := EvidenceFrom(g.Provider(), command, Response{})
		evidence.Source = "reconciliation"
		evidence.Detail = "command type is not a reconcilable mutation"
		return ReconcileResult{
			Status:   ReconcileAmbiguous,
			Evidence: evidence,
			Reason:   evidence.Detail,
		}, nil
	}
}

func (g *GitHubExecutor) validate(command Command) error {
	if g.baseURL == "" || g.token == "" {
		return fmt.Errorf("GitHub executor is not configured")
	}
	return command.Repository.Validate()
}

func (g *GitHubExecutor) readFile(ctx context.Context, command Command) (Response, error) {
	if err := validateFilePath(command.Path); err != nil {
		return Response{}, err
	}
	query := url.Values{}
	if command.Reference != nil {
		query.Set("ref", *command.Reference)
	}
	var out githubContent
	if err := g.doJSON(ctx, http.MethodGet, g.contentsPath(command), query, nil, &out); err != nil {
		return Response{}, err
	}
	if out.Type != "" && out.Type != "file" {
		return Response{}, fmt.Errorf("GitHub contents response is not a file")
	}
	content, err := decodeGitHubContent(out.Encoding, out.Content)
	if err != nil {
		return Response{}, err
	}
	if out.Path == "" {
		out.Path = command.Path
	}
	if out.SHA == "" {
		return Response{}, fmt.Errorf("GitHub file response missing sha")
	}
	return Response{Type: "file", Value: FileContent{
		Path: out.Path, Content: intsFromBytes(content), SHA: out.SHA,
	}}, nil
}

func (g *GitHubExecutor) createBranch(ctx context.Context, command Command) (Response, error) {
	if err := requireText("branch name", command.Name); err != nil {
		return Response{}, err
	}
	if err := requireText("branch source", command.From); err != nil {
		return Response{}, err
	}
	source, err := g.getRef(ctx, command.Repository, command.From)
	if err != nil {
		return Response{}, err
	}
	var out githubRef
	if err := g.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/git/refs", command.Repository.Owner, command.Repository.Name),
		nil,
		map[string]any{
			"ref": "refs/heads/" + command.Name,
			"sha": source.Object.SHA,
		},
		&out,
	); err != nil {
		return Response{}, err
	}
	name := strings.TrimPrefix(out.Ref, "refs/heads/")
	if name == "" {
		name = command.Name
	}
	if out.Object.SHA == "" {
		return Response{}, fmt.Errorf("GitHub created ref response missing sha")
	}
	return Response{Type: "branch", Value: BranchRef{Name: name, CommitID: out.Object.SHA}}, nil
}

func (g *GitHubExecutor) writeFile(ctx context.Context, command Command) (Response, error) {
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
	payload := map[string]any{
		"message": command.Message,
		"content": base64.StdEncoding.EncodeToString(content),
		"branch":  command.Branch,
	}
	if command.ExpectedBlobSHA != nil {
		payload["sha"] = *command.ExpectedBlobSHA
	}
	var out struct {
		Content struct {
			SHA string `json:"sha"`
		} `json:"content"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := g.doJSON(ctx, http.MethodPut, g.contentsPath(command), nil, payload, &out); err != nil {
		return Response{}, err
	}
	if out.Commit.SHA == "" {
		return Response{}, fmt.Errorf("GitHub commit response missing sha")
	}
	return Response{Type: "commit", Value: CommitRef{ID: out.Commit.SHA}}, nil
}

func (g *GitHubExecutor) createChange(ctx context.Context, command Command) (Response, error) {
	if err := requireText("pull request head", command.Head); err != nil {
		return Response{}, err
	}
	if err := requireText("pull request base", command.Base); err != nil {
		return Response{}, err
	}
	if err := requireText("pull request title", command.Title); err != nil {
		return Response{}, err
	}
	var out githubPull
	if err := g.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/pulls", command.Repository.Owner, command.Repository.Name),
		nil,
		map[string]any{
			"head":  command.Head,
			"base":  command.Base,
			"title": command.Title,
			"body":  command.Body,
		},
		&out,
	); err != nil {
		return Response{}, err
	}
	return Response{Type: "change", Value: githubPullResponse(out)}, nil
}

func (g *GitHubExecutor) reviewChange(ctx context.Context, command Command) (Response, error) {
	event := map[string]string{
		"approve":         "APPROVE",
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
		ID uint64 `json:"id"`
	}
	if err := g.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews",
			command.Repository.Owner, command.Repository.Name, command.Number),
		nil, payload, &out,
	); err != nil {
		return Response{}, err
	}
	if out.ID == 0 {
		return Response{}, fmt.Errorf("GitHub review response missing id")
	}
	id := out.ID
	return Response{Type: "review", Value: ReviewRef{ID: &id}}, nil
}

func (g *GitHubExecutor) mergeChange(ctx context.Context, command Command) (Response, error) {
	method := map[string]string{
		"merge":  "merge",
		"squash": "squash",
		"rebase": "rebase",
	}[command.Method]
	if method == "" {
		return Response{}, fmt.Errorf("unsupported GitHub merge method %q", command.Method)
	}
	payload := map[string]any{"merge_method": method}
	if command.ExpectedHeadSHA != nil {
		payload["sha"] = *command.ExpectedHeadSHA
	}
	var out struct {
		SHA     string `json:"sha"`
		Merged  bool   `json:"merged"`
		Message string `json:"message"`
	}
	if err := g.doJSON(ctx, http.MethodPut,
		fmt.Sprintf("/repos/%s/%s/pulls/%d/merge",
			command.Repository.Owner, command.Repository.Name, command.Number),
		nil, payload, &out,
	); err != nil {
		return Response{}, err
	}
	return Response{Type: "merge", Value: MergeResult{Merged: out.Merged}}, nil
}

func (g *GitHubExecutor) listChecks(ctx context.Context, command Command) (Response, error) {
	if command.Reference == nil {
		return Response{}, fmt.Errorf("check reference is required")
	}
	if err := requireText("check reference", *command.Reference); err != nil {
		return Response{}, err
	}
	var out struct {
		CheckRuns []struct {
			ID         uint64  `json:"id"`
			Name       string  `json:"name"`
			Status     string  `json:"status"`
			Conclusion *string `json:"conclusion"`
			HTMLURL    *string `json:"html_url"`
		} `json:"check_runs"`
	}
	if err := g.doJSON(ctx, http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs",
			command.Repository.Owner, command.Repository.Name, encodePath(*command.Reference)),
		url.Values{"per_page": []string{"100"}}, nil, &out,
	); err != nil {
		return Response{}, err
	}
	checks := make([]CheckRun, 0, len(out.CheckRuns))
	for _, item := range out.CheckRuns {
		state := item.Status
		if item.Conclusion != nil && *item.Conclusion != "" {
			state = *item.Conclusion
		}
		checks = append(checks, CheckRun{
			ID: strconv.FormatUint(item.ID, 10), Context: item.Name,
			State: state, TargetURL: item.HTMLURL,
		})
	}
	return Response{Type: "checks", Value: checks}, nil
}

func (g *GitHubExecutor) reconcileCreateBranch(ctx context.Context, command Command) (ReconcileResult, error) {
	status, body, err := g.doRaw(ctx, http.MethodGet, g.refPath(command.Repository, command.Name), nil, nil)
	if err != nil {
		return ReconcileResult{}, err
	}
	evidence := EvidenceFrom(g.Provider(), command, Response{})
	evidence.Source = "reconciliation"
	if status == http.StatusNotFound {
		evidence.Detail = "branch absent"
		return ReconcileResult{Status: ReconcileNotApplied, Evidence: evidence, Reason: evidence.Detail}, nil
	}
	if status < 200 || status >= 300 {
		return ReconcileResult{}, fmt.Errorf("GitHub branch probe returned HTTP %d: %s", status, truncate(string(body), 512))
	}
	var ref githubRef
	if err := json.Unmarshal(body, &ref); err != nil {
		return ReconcileResult{}, fmt.Errorf("decode GitHub branch probe: %w", err)
	}
	if ref.Object.SHA == "" {
		return ReconcileResult{}, fmt.Errorf("GitHub branch probe missing sha")
	}
	response := Response{Type: "branch", Value: BranchRef{Name: command.Name, CommitID: ref.Object.SHA}}
	evidence = EvidenceFrom(g.Provider(), command, response)
	evidence.Source = "reconciliation"
	evidence.Detail = "branch exists"
	return ReconcileResult{Status: ReconcileApplied, Response: &response, Evidence: evidence}, nil
}

func (g *GitHubExecutor) reconcileWriteFile(ctx context.Context, command Command) (ReconcileResult, error) {
	if err := validateFilePath(command.Path); err != nil {
		return ReconcileResult{}, err
	}
	query := url.Values{"ref": []string{command.Branch}}
	status, body, err := g.doRaw(ctx, http.MethodGet, g.contentsPath(command), query, nil)
	if err != nil {
		return ReconcileResult{}, err
	}
	evidence := EvidenceFrom(g.Provider(), command, Response{})
	evidence.Source = "reconciliation"
	if status == http.StatusNotFound {
		evidence.Detail = "file absent at target branch"
		return ReconcileResult{Status: ReconcileNotApplied, Evidence: evidence, Reason: evidence.Detail}, nil
	}
	if status < 200 || status >= 300 {
		return ReconcileResult{}, fmt.Errorf("GitHub file probe returned HTTP %d: %s", status, truncate(string(body), 512))
	}
	var file githubContent
	if err := json.Unmarshal(body, &file); err != nil {
		return ReconcileResult{}, fmt.Errorf("decode GitHub file probe: %w", err)
	}
	evidence.BlobSHA = file.SHA
	observed, err := decodeGitHubContent(file.Encoding, file.Content)
	if err != nil {
		return ReconcileResult{}, err
	}
	expected, err := bytesFromInts(command.Content)
	if err != nil {
		return ReconcileResult{}, err
	}
	if !bytes.Equal(observed, expected) {
		evidence.Detail = "target file exists but content differs from requested write"
		return ReconcileResult{Status: ReconcileAmbiguous, Evidence: evidence, Reason: evidence.Detail}, nil
	}
	ref, err := g.getRef(ctx, command.Repository, command.Branch)
	if err != nil {
		evidence.Detail = "file matches but branch head could not be proven"
		return ReconcileResult{Status: ReconcileAmbiguous, Evidence: evidence, Reason: evidence.Detail}, nil
	}
	response := Response{Type: "commit", Value: CommitRef{ID: ref.Object.SHA}}
	evidence = EvidenceFrom(g.Provider(), command, response)
	evidence.Source = "reconciliation"
	evidence.BlobSHA = file.SHA
	evidence.Detail = "target file content matches requested write"
	return ReconcileResult{Status: ReconcileApplied, Response: &response, Evidence: evidence}, nil
}

func (g *GitHubExecutor) reconcileCreateChange(ctx context.Context, command Command) (ReconcileResult, error) {
	query := url.Values{
		"state":    []string{"all"},
		"head":     []string{command.Repository.Owner + ":" + command.Head},
		"base":     []string{command.Base},
		"per_page": []string{"100"},
	}
	status, body, err := g.doRaw(ctx, http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/pulls", command.Repository.Owner, command.Repository.Name),
		query, nil)
	if err != nil {
		return ReconcileResult{}, err
	}
	if status < 200 || status >= 300 {
		return ReconcileResult{}, fmt.Errorf("GitHub pull probe returned HTTP %d: %s", status, truncate(string(body), 512))
	}
	var pulls []githubPull
	if err := json.Unmarshal(body, &pulls); err != nil {
		return ReconcileResult{}, fmt.Errorf("decode GitHub pull probe: %w", err)
	}
	evidence := EvidenceFrom(g.Provider(), command, Response{})
	evidence.Source = "reconciliation"
	if len(pulls) == 0 {
		evidence.Detail = "pull request absent for base/head pair"
		return ReconcileResult{Status: ReconcileNotApplied, Evidence: evidence, Reason: evidence.Detail}, nil
	}
	exact := make([]githubPull, 0, 1)
	for _, pull := range pulls {
		if pull.Head.Ref == command.Head && pull.Base.Ref == command.Base && pull.Title == command.Title {
			exact = append(exact, pull)
		}
	}
	if len(exact) != 1 {
		evidence.Detail = "pull request could not be attributed uniquely to requested base/head/title"
		return ReconcileResult{Status: ReconcileAmbiguous, Evidence: evidence, Reason: evidence.Detail}, nil
	}
	response := Response{Type: "change", Value: githubPullResponse(exact[0])}
	evidence = EvidenceFrom(g.Provider(), command, response)
	evidence.Source = "reconciliation"
	evidence.Detail = "pull request matches requested base/head/title"
	return ReconcileResult{Status: ReconcileApplied, Response: &response, Evidence: evidence}, nil
}

func (g *GitHubExecutor) reconcileReviewChange(ctx context.Context, command Command) (ReconcileResult, error) {
	status, body, err := g.doRaw(ctx, http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews",
			command.Repository.Owner, command.Repository.Name, command.Number),
		url.Values{"per_page": []string{"100"}}, nil)
	if err != nil {
		return ReconcileResult{}, err
	}
	if status < 200 || status >= 300 {
		return ReconcileResult{}, fmt.Errorf("GitHub review probe returned HTTP %d: %s", status, truncate(string(body), 512))
	}
	var reviews []struct {
		ID       uint64 `json:"id"`
		Body     string `json:"body"`
		CommitID string `json:"commit_id"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal(body, &reviews); err != nil {
		return ReconcileResult{}, fmt.Errorf("decode GitHub reviews: %w", err)
	}
	wantState := map[string]string{
		"approve":         "APPROVED",
		"request_changes": "CHANGES_REQUESTED",
		"comment":         "COMMENTED",
	}[command.Event]
	evidence := EvidenceFrom(g.Provider(), command, Response{})
	evidence.Source = "reconciliation"
	var matches []uint64
	for _, review := range reviews {
		if review.State != wantState || review.Body != command.Body {
			continue
		}
		if command.CommitID != nil && review.CommitID != *command.CommitID {
			continue
		}
		matches = append(matches, review.ID)
	}
	if len(matches) != 1 {
		evidence.Detail = "review could not be attributed uniquely to the requested mutation"
		return ReconcileResult{Status: ReconcileAmbiguous, Evidence: evidence, Reason: evidence.Detail}, nil
	}
	id := matches[0]
	response := Response{Type: "review", Value: ReviewRef{ID: &id}}
	evidence = EvidenceFrom(g.Provider(), command, response)
	evidence.Source = "reconciliation"
	evidence.Detail = "exact matching review observed"
	return ReconcileResult{Status: ReconcileApplied, Response: &response, Evidence: evidence}, nil
}

func (g *GitHubExecutor) reconcileMergeChange(ctx context.Context, command Command) (ReconcileResult, error) {
	status, body, err := g.doRaw(ctx, http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/pulls/%d/merge",
			command.Repository.Owner, command.Repository.Name, command.Number),
		nil, nil)
	if err != nil {
		return ReconcileResult{}, err
	}
	evidence := EvidenceFrom(g.Provider(), command, Response{})
	evidence.Source = "reconciliation"
	if command.ExpectedHeadSHA != nil {
		evidence.HeadSHA = *command.ExpectedHeadSHA
	}
	switch status {
	case http.StatusNoContent:
		merged := true
		response := Response{Type: "merge", Value: MergeResult{Merged: true}}
		evidence.Merged = &merged
		evidence.Detail = "provider reports pull request merged"
		return ReconcileResult{Status: ReconcileApplied, Response: &response, Evidence: evidence}, nil
	case http.StatusNotFound:
		evidence.Detail = "provider reports pull request not merged"
		return ReconcileResult{Status: ReconcileNotApplied, Evidence: evidence, Reason: evidence.Detail}, nil
	default:
		return ReconcileResult{}, fmt.Errorf("GitHub merge probe returned HTTP %d: %s", status, truncate(string(body), 512))
	}
}

func (g *GitHubExecutor) getRef(ctx context.Context, repository RepositoryRef, branch string) (githubRef, error) {
	var out githubRef
	if err := g.doJSON(ctx, http.MethodGet, g.refPath(repository, branch), nil, nil, &out); err != nil {
		return githubRef{}, err
	}
	if out.Object.SHA == "" {
		return githubRef{}, fmt.Errorf("GitHub ref response missing sha")
	}
	return out, nil
}

func (g *GitHubExecutor) refPath(repository RepositoryRef, branch string) string {
	return fmt.Sprintf("/repos/%s/%s/git/ref/%s",
		repository.Owner, repository.Name, encodePath("heads/"+branch))
}

func (g *GitHubExecutor) contentsPath(command Command) string {
	return fmt.Sprintf("/repos/%s/%s/contents/%s",
		command.Repository.Owner, command.Repository.Name, encodePath(command.Path))
}

func (g *GitHubExecutor) doJSON(
	ctx context.Context, method, path string, query url.Values, payload any, out any,
) error {
	status, body, err := g.doRaw(ctx, method, path, query, payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("GitHub returned HTTP %d: %s", status, truncate(string(body), 512))
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func (g *GitHubExecutor) doRaw(
	ctx context.Context, method, path string, query url.Values, payload any,
) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal GitHub request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	target := g.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, fmt.Errorf("create GitHub request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("GitHub transport: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read GitHub response: %w", err)
	}
	return resp.StatusCode, responseBody, nil
}

type githubRef struct {
	Ref    string `json:"ref"`
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type githubContent struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type githubPull struct {
	Number  uint64 `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	URL     string `json:"url"`
	State   string `json:"state"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func githubPullResponse(pull githubPull) ChangeRef {
	rawURL := pull.HTMLURL
	if rawURL == "" {
		rawURL = pull.URL
	}
	var responseURL *string
	if rawURL != "" {
		responseURL = &rawURL
	}
	return ChangeRef{
		Number: pull.Number, URL: responseURL,
		Head: pull.Head.Ref, HeadSHA: pull.Head.SHA,
		Base: pull.Base.Ref, State: pull.State,
	}
}

func decodeGitHubContent(encoding, value string) ([]byte, error) {
	if encoding == "" || strings.EqualFold(encoding, "base64") {
		decoded, err := base64.StdEncoding.DecodeString(stripWhitespace(value))
		if err != nil {
			return nil, fmt.Errorf("decode GitHub file content: %w", err)
		}
		return decoded, nil
	}
	return []byte(value), nil
}
