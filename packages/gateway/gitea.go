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

	"github.com/ai-dev-control-plane/forge"
)

// GiteaGateway implements the provider-neutral forge contract for Gitea and
// Forgejo-compatible API v1 instances.
type GiteaGateway struct {
	httpClient       *http.Client
	apiBaseURL       string
	draftTitlePrefix string
}

// NewGiteaGateway creates a Gitea/Forgejo-compatible forge adapter.
//
// instanceURL may be either an instance root (for example
// https://code.example.com) or an API root ending in /api/v1.
func NewGiteaGateway(instanceURL string) *GiteaGateway {
	base := strings.TrimRight(strings.TrimSpace(instanceURL), "/")
	if !strings.HasSuffix(base, "/api/v1") {
		base += "/api/v1"
	}
	return &GiteaGateway{
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		apiBaseURL:       base,
		draftTitlePrefix: "WIP: ",
	}
}

// WithDraftTitlePrefix configures the instance's work-in-progress title prefix.
// Gitea and Forgejo infer draft state from configured title prefixes rather
// than accepting a draft field when creating a pull request.
func (g *GiteaGateway) WithDraftTitlePrefix(prefix string) *GiteaGateway {
	if prefix = strings.TrimSpace(prefix); prefix != "" {
		g.draftTitlePrefix = prefix + " "
	}
	return g
}

// Name identifies the forge provider.
func (g *GiteaGateway) Name() string { return "gitea" }

// giteaPullRequest is the subset of Gitea's pull request representation needed
// by the provider-neutral contract.
type giteaPullRequest struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	State          string `json:"state"`
	HTMLURL        string `json:"html_url"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
}

type giteaCreatePullRequest struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}

type giteaMergePullRequest struct {
	Do                string `json:"do"`
	HeadCommitID      string `json:"head_commit_id,omitempty"`
	MergeTitleField   string `json:"merge_title_field,omitempty"`
	MergeMessageField string `json:"merge_message_field,omitempty"`
}

// OpenChange implements forge.Provider.
func (g *GiteaGateway) OpenChange(ctx context.Context, credential forge.Credential, repository forge.Repository, req forge.OpenChangeRequest) (*forge.Change, error) {
	if err := forge.ValidateOpenChangeRequest(repository, req); err != nil {
		return nil, err
	}
	if err := validateGiteaRepository(repository); err != nil {
		return nil, err
	}

	title := req.Title
	if req.Draft && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(title)), strings.ToLower(strings.TrimSpace(g.draftTitlePrefix))) {
		title = g.draftTitlePrefix + title
	}

	payload := giteaCreatePullRequest{
		Title: title,
		Body:  req.Body,
		Head:  req.Head,
		Base:  req.Base,
	}
	var result giteaPullRequest
	if err := g.doJSON(
		ctx,
		http.MethodPost,
		g.repositoryPath(repository)+"/pulls",
		credential,
		payload,
		&result,
	); err != nil {
		return nil, fmt.Errorf("create gitea pull request: %w", err)
	}

	return g.changeFromPullRequest(result), nil
}

// MergeChange implements forge.Provider. Gitea's merge endpoint returns an
// empty success response, so the adapter reads the PR afterward to obtain the
// authoritative merged state and merge revision.
func (g *GiteaGateway) MergeChange(ctx context.Context, credential forge.Credential, repository forge.Repository, number int, req forge.MergeChangeRequest) (*forge.MergeResult, error) {
	if err := forge.ValidateMergeChangeRequest(repository, number, req); err != nil {
		return nil, err
	}
	if err := validateGiteaRepository(repository); err != nil {
		return nil, err
	}

	payload := giteaMergePullRequest{
		Do:                string(forge.NormalizeMergeMethod(req.Method)),
		HeadCommitID:      req.ExpectedHeadRevision,
		MergeTitleField:   req.CommitTitle,
		MergeMessageField: req.CommitMessage,
	}
	path := fmt.Sprintf("%s/pulls/%d", g.repositoryPath(repository), number)
	if err := g.doJSON(ctx, http.MethodPost, path+"/merge", credential, payload, nil); err != nil {
		return nil, fmt.Errorf("merge gitea pull request: %w", err)
	}

	var merged giteaPullRequest
	if err := g.doJSON(ctx, http.MethodGet, path, credential, nil, &merged); err != nil {
		return nil, fmt.Errorf("read merged gitea pull request: %w", err)
	}
	if !merged.Merged {
		return nil, fmt.Errorf("%w: pull request %d did not report merged after merge request", forge.ErrConflict, number)
	}
	if strings.TrimSpace(merged.MergeCommitSHA) == "" {
		return nil, fmt.Errorf("%w: pull request %d has no merged revision", forge.ErrConflict, number)
	}

	return &forge.MergeResult{
		Merged:   true,
		Revision: merged.MergeCommitSHA,
		Message:  "merged",
	}, nil
}

func (g *GiteaGateway) repositoryPath(repository forge.Repository) string {
	return "/repos/" + url.PathEscape(repository.Namespace) + "/" + url.PathEscape(repository.Name)
}

func (g *GiteaGateway) changeFromPullRequest(pr giteaPullRequest) *forge.Change {
	state := forge.ChangeState(pr.State)
	if pr.Merged {
		state = forge.ChangeStateMerged
	}
	if state == "" {
		state = forge.ChangeStateOpen
	}
	return &forge.Change{
		Number:       pr.Number,
		Title:        pr.Title,
		Body:         pr.Body,
		URL:          pr.HTMLURL,
		State:        state,
		Head:         pr.Head.Ref,
		Base:         pr.Base.Ref,
		HeadRevision: pr.Head.SHA,
		Draft:        pr.Draft,
	}
}

func validateGiteaRepository(repository forge.Repository) error {
	if strings.Contains(repository.Namespace, "/") {
		return fmt.Errorf("%w: Gitea repository namespace must be a single owner or organization", forge.ErrInvalidRequest)
	}
	return nil
}

func (g *GiteaGateway) doJSON(
	ctx context.Context,
	method string,
	path string,
	credential forge.Credential,
	input any,
	output any,
) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("marshal gitea request: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, g.apiBaseURL+path, body)
	if err != nil {
		return fmt.Errorf("create gitea request: %w", err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := strings.TrimSpace(credential.Token); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gitea request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeGiteaError(resp)
	}
	if output == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
		return fmt.Errorf("decode gitea response: %w", err)
	}
	return nil
}

func decodeGiteaError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	message := strings.TrimSpace(string(data))
	var apiError struct {
		Message string `json:"message"`
	}
	if len(data) > 0 && json.Unmarshal(data, &apiError) == nil && strings.TrimSpace(apiError.Message) != "" {
		message = strings.TrimSpace(apiError.Message)
	}
	if message == "" {
		message = resp.Status
	}

	var category error
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		category = forge.ErrInvalidRequest
	case http.StatusNotFound:
		category = forge.ErrNotFound
	case http.StatusMethodNotAllowed, http.StatusConflict, http.StatusLocked:
		category = forge.ErrConflict
	default:
		return fmt.Errorf("gitea API %s: %s", resp.Status, message)
	}
	return fmt.Errorf("%w: gitea API %s: %s", category, resp.Status, message)
}

var _ forge.Provider = (*GiteaGateway)(nil)
