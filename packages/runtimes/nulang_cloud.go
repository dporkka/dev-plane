package runtimes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type NulangCloudProvider struct {
	baseURL       string
	internalToken string
	client        *http.Client
}

type nulangCloudCreateRequest struct {
	Spec      SandboxSpec              `json:"spec"`
	Workspace nulangCloudWorkspaceSpec `json:"workspace"`
}

type nulangCloudWorkspaceSpec struct {
	RepositoryID string `json:"repository_id"`
	CloneURL     string `json:"clone_url,omitempty"`
	Branch       string `json:"branch,omitempty"`
	BaseBranch   string `json:"base_branch,omitempty"`
	WorktreeName string `json:"worktree_name,omitempty"`
}

type nulangCloudCreateResponse struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	PreviewURL string    `json:"preview_url,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

var _ Provider = (*NulangCloudProvider)(nil)

func NewNulangCloudProvider(baseURL, internalToken string) (*NulangCloudProvider, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("Nulang Cloud runtime requires NULANG_CLOUD_URL")
	}
	return &NulangCloudProvider{
		baseURL:       baseURL,
		internalToken: internalToken,
		client:        &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func NewNulangCloudProviderFromEnv() (*NulangCloudProvider, error) {
	return NewNulangCloudProvider(os.Getenv("NULANG_CLOUD_URL"), os.Getenv("NULANG_CLOUD_TOKEN"))
}

func (p *NulangCloudProvider) WithHTTPClient(client *http.Client) *NulangCloudProvider {
	if client != nil {
		p.client = client
	}
	return p
}

func (p *NulangCloudProvider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if p.internalToken != "" {
		req.Header.Set("X-Internal-Auth-Token", p.internalToken)
	}
	return req, nil
}

func (p *NulangCloudProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	if len(req.Env) > 0 {
		return nil, fmt.Errorf("Nulang Cloud runtime rejects inline environment values; use sandbox secret references or brokered capabilities")
	}

	spec := DefaultDevPlaneSandboxSpec()
	if req.Sandbox != nil {
		spec = normalizeSandboxSpec(*req.Sandbox)
	}

	payload := nulangCloudCreateRequest{
		Spec: spec,
		Workspace: nulangCloudWorkspaceSpec{
			RepositoryID: req.RepositoryID,
			CloneURL:     req.CloneURL,
			Branch:       req.Branch,
			BaseBranch:   req.BaseBranch,
			WorktreeName: req.WorktreeName,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Nulang Cloud sandbox request: %w", err)
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/v1/sandboxes", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("create Nulang Cloud sandbox: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, p.readError(resp)
	}

	var created nulangCloudCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("decode Nulang Cloud sandbox response: %w", err)
	}
	if created.ID == "" {
		return nil, fmt.Errorf("Nulang Cloud sandbox response missing id")
	}
	if created.Status == "" {
		created.Status = "pending"
	}
	if created.CreatedAt.IsZero() {
		created.CreatedAt = time.Now().UTC()
	}

	return &Session{
		ID:          created.ID,
		WorkspaceID: req.RepositoryID,
		Status:      created.Status,
		PreviewURL:  created.PreviewURL,
		Provider:    "nulang-cloud",
		CreatedAt:   created.CreatedAt,
	}, nil
}

func (p *NulangCloudProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	httpReq, err := p.newRequest(ctx, http.MethodDelete, p.sandboxPath(sessionID), nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("destroy Nulang Cloud sandbox: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return p.readError(resp)
	}
	return nil
}

func (p *NulangCloudProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd Command) (*CommandResult, error) {
	body, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}
	httpReq, err := p.newRequest(ctx, http.MethodPost, p.sandboxPath(sessionID)+"/commands", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute Nulang Cloud command: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.readError(resp)
	}
	var result CommandResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode Nulang Cloud command result: %w", err)
	}
	return &result, nil
}

func (p *NulangCloudProvider) ReadFile(ctx context.Context, sessionID, filePath string) ([]byte, error) {
	httpReq, err := p.newRequest(ctx, http.MethodGet, p.sandboxPath(sessionID)+"/files/"+escapePath(filePath), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("read Nulang Cloud file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.readError(resp)
	}
	return io.ReadAll(resp.Body)
}

func (p *NulangCloudProvider) WriteFile(ctx context.Context, sessionID, filePath string, data []byte) error {
	httpReq, err := p.newRequest(ctx, http.MethodPut, p.sandboxPath(sessionID)+"/files/"+escapePath(filePath), bytes.NewReader(data))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/octet-stream")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("write Nulang Cloud file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return p.readError(resp)
	}
	return nil
}

func (p *NulangCloudProvider) ApplyPatch(ctx context.Context, sessionID, patch string) error {
	httpReq, err := p.newRequest(ctx, http.MethodPost, p.sandboxPath(sessionID)+"/patches", strings.NewReader(patch))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "text/x-diff")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("apply Nulang Cloud patch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return p.readError(resp)
	}
	return nil
}

func (p *NulangCloudProvider) Snapshot(ctx context.Context, sessionID string) (*Snapshot, error) {
	httpReq, err := p.newRequest(ctx, http.MethodPost, p.sandboxPath(sessionID)+"/snapshots", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("snapshot Nulang Cloud sandbox: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, p.readError(resp)
	}
	var snap Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode Nulang Cloud snapshot: %w", err)
	}
	return &snap, nil
}

func (p *NulangCloudProvider) Restore(ctx context.Context, sessionID string, snap *Snapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	httpReq, err := p.newRequest(ctx, http.MethodPost, p.sandboxPath(sessionID)+"/restore", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("restore Nulang Cloud sandbox: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return p.readError(resp)
	}
	return nil
}

func (p *NulangCloudProvider) GetStatus(ctx context.Context, sessionID string) (*SessionStatus, error) {
	httpReq, err := p.newRequest(ctx, http.MethodGet, p.sandboxPath(sessionID)+"/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("get Nulang Cloud sandbox status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.readError(resp)
	}
	var status SessionStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode Nulang Cloud status: %w", err)
	}
	return &status, nil
}

func (p *NulangCloudProvider) StreamLogs(ctx context.Context, sessionID string) (<-chan LogLine, error) {
	httpReq, err := p.newRequest(ctx, http.MethodGet, p.sandboxPath(sessionID)+"/logs", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("stream Nulang Cloud logs: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, p.readError(resp)
	}

	out := make(chan LogLine)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var item LogLine
			if err := json.Unmarshal([]byte(data), &item); err != nil {
				continue
			}
			select {
			case out <- item:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *NulangCloudProvider) sandboxPath(sessionID string) string {
	return "/v1/sandboxes/" + url.PathEscape(sessionID)
}

func (p *NulangCloudProvider) readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return ErrSessionNotFound
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ErrCommandTimeout
	default:
		return fmt.Errorf("Nulang Cloud returned %d: %s", resp.StatusCode, message)
	}
}
