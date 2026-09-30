package runtimes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NulangCloudProvider implements the Dev Plane runtime contract against the
// Nulang Cloud workspace API. Nulang Cloud remains responsible for physical
// isolation, resource enforcement, secrets injection, snapshots, and runtime
// metering; Dev Plane remains responsible for task policy and approvals.
//
// The provider is intentionally opt-in until the Nulang Cloud workspace API
// passes the same conformance suite as the Docker/remote providers.
type NulangCloudProvider struct {
	*RemoteProvider
	internalToken string
}

func NewNulangCloudProvider(baseURL, internalToken string) *NulangCloudProvider {
	remote := NewRemoteProvider(baseURL, "")
	provider := &NulangCloudProvider{
		RemoteProvider: remote,
		internalToken:  internalToken,
	}
	provider.RemoteProvider.client = provider.wrapHTTPClient(&http.Client{Timeout: 120 * time.Second})
	return provider
}

func (p *NulangCloudProvider) WithHTTPClient(client *http.Client) *NulangCloudProvider {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	p.RemoteProvider.client = p.wrapHTTPClient(client)
	return p
}

func (p *NulangCloudProvider) wrapHTTPClient(client *http.Client) *http.Client {
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = &nulangAuthTransport{
		base:  base,
		token: p.internalToken,
	}
	return &clone
}

type nulangAuthTransport struct {
	base  http.RoundTripper
	token string
}

func (t *nulangAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if t.token != "" {
		clone.Header.Set("X-Internal-Auth-Token", t.token)
	}
	return t.base.RoundTrip(clone)
}

func (p *NulangCloudProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal Nulang create request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.RemoteProvider.baseURL+"/v1/workspaces", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(req.IdempotencyKey); key != "" {
		httpReq.Header.Set("Idempotency-Key", key)
	}

	resp, err := p.RemoteProvider.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("create Nulang workspace request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, p.RemoteProvider.readError(resp)
	}

	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("decode Nulang workspace response: %w", err)
	}
	return &session, nil
}

func (p *NulangCloudProvider) GetUsage(ctx context.Context, sessionID string) (*RuntimeUsage, error) {
	path := "/v1/workspaces/" + url.PathEscape(sessionID) + "/usage"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.RemoteProvider.baseURL+path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.RemoteProvider.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Nulang runtime usage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrSessionNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, p.RemoteProvider.readError(resp)
	}

	var usage RuntimeUsage
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		return nil, fmt.Errorf("decode Nulang runtime usage: %w", err)
	}
	return &usage, nil
}


var _ Provider = (*NulangCloudProvider)(nil)
var _ UsageProvider = (*NulangCloudProvider)(nil)
