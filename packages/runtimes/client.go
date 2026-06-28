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
	"strings"
	"time"
)

// RemoteProvider is an HTTP client that implements the Provider interface
// by talking to a runner service.
type RemoteProvider struct {
	baseURL   string
	authToken string
	client    *http.Client
}

// NewRemoteProvider creates a client for the runner service at baseURL.
// baseURL should be the scheme+host of the runner, e.g. http://localhost:8082.
func NewRemoteProvider(baseURL, authToken string) *RemoteProvider {
	baseURL = strings.TrimRight(baseURL, "/")
	return &RemoteProvider{
		baseURL:   baseURL,
		authToken: authToken,
		client:    &http.Client{Timeout: 120 * time.Second},
	}
}

// WithHTTPClient overrides the default HTTP client.
func (p *RemoteProvider) WithHTTPClient(client *http.Client) *RemoteProvider {
	p.client = client
	return p
}

func (p *RemoteProvider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if p.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.authToken)
	}
	return req, nil
}

// CreateWorkspace provisions a new workspace session on the runner.
func (p *RemoteProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal create request: %w", err)
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/v1/workspaces", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("create workspace request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, p.readError(resp)
	}

	var sess Session
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		return nil, fmt.Errorf("decode create workspace response: %w", err)
	}
	return &sess, nil
}

// DestroyWorkspace tears down a workspace session on the runner.
func (p *RemoteProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	path := "/v1/workspaces/" + url.PathEscape(sessionID)
	httpReq, err := p.newRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("destroy workspace request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return p.readError(resp)
	}
	return nil
}

// ExecuteCommand runs a command in a workspace session on the runner.
func (p *RemoteProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd Command) (*CommandResult, error) {
	body, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}

	path := "/v1/workspaces/" + url.PathEscape(sessionID) + "/commands"
	httpReq, err := p.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute command request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, p.readError(resp)
	}

	var result CommandResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode command result: %w", err)
	}
	return &result, nil
}

// ReadFile reads a file from a workspace session on the runner.
func (p *RemoteProvider) ReadFile(ctx context.Context, sessionID, filePath string) ([]byte, error) {
	path := "/v1/workspaces/" + url.PathEscape(sessionID) + "/files/" + escapePath(filePath)
	httpReq, err := p.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("read file request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrSessionNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, p.readError(resp)
	}

	return io.ReadAll(resp.Body)
}

// WriteFile writes data to a file in a workspace session on the runner.
func (p *RemoteProvider) WriteFile(ctx context.Context, sessionID, filePath string, data []byte) error {
	path := "/v1/workspaces/" + url.PathEscape(sessionID) + "/files/" + escapePath(filePath)
	httpReq, err := p.newRequest(ctx, http.MethodPut, path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/octet-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("write file request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrSessionNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return p.readError(resp)
	}
	return nil
}

// ApplyPatch applies a unified diff patch to a workspace session on the runner.
func (p *RemoteProvider) ApplyPatch(ctx context.Context, sessionID, patch string) error {
	path := "/v1/workspaces/" + url.PathEscape(sessionID) + "/patches"
	httpReq, err := p.newRequest(ctx, http.MethodPost, path, strings.NewReader(patch))
	if err != nil {
		return err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("apply patch request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrSessionNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return p.readError(resp)
	}
	return nil
}

// Snapshot captures the current state of a workspace session on the runner.
func (p *RemoteProvider) Snapshot(ctx context.Context, sessionID string) (*Snapshot, error) {
	path := "/v1/workspaces/" + url.PathEscape(sessionID) + "/snapshot"
	httpReq, err := p.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("snapshot request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrSessionNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, p.readError(resp)
	}

	var snap Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	return &snap, nil
}

// Restore restores a workspace session from a snapshot on the runner.
func (p *RemoteProvider) Restore(ctx context.Context, sessionID string, snap *Snapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	path := "/v1/workspaces/" + url.PathEscape(sessionID) + "/restore"
	httpReq, err := p.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("restore request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrSessionNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return p.readError(resp)
	}
	return nil
}

// GetStatus returns the current status of a workspace session on the runner.
func (p *RemoteProvider) GetStatus(ctx context.Context, sessionID string) (*SessionStatus, error) {
	path := "/v1/workspaces/" + url.PathEscape(sessionID) + "/status"
	httpReq, err := p.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("status request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrSessionNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, p.readError(resp)
	}

	var status SessionStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode status: %w", err)
	}
	return &status, nil
}

// StreamLogs returns a channel of log lines from a workspace session on the runner.
func (p *RemoteProvider) StreamLogs(ctx context.Context, sessionID string) (<-chan LogLine, error) {
	path := "/v1/workspaces/" + url.PathEscape(sessionID) + "/logs"
	reqURL := p.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	if p.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.authToken)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stream logs request: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrSessionNotFound
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
			select {
			case <-ctx.Done():
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					return
				}
				return
			}
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var logLine LogLine
			if err := json.Unmarshal([]byte(data), &logLine); err != nil {
				continue
			}
			select {
			case out <- logLine:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

func (p *RemoteProvider) readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound && strings.Contains(strings.ToLower(msg), "session not found") {
		return ErrSessionNotFound
	}
	return fmt.Errorf("runner returned %d: %s", resp.StatusCode, msg)
}

func escapePath(p string) string {
	// Ensure leading slash does not get stripped by chi wildcard.
	p = strings.TrimPrefix(p, "/")
	return url.PathEscape(p)
}
