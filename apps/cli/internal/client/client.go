// Package client provides a thin HTTP client for the Dev Plane API.
package client

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

// Client is a minimal authenticated HTTP client for the Dev Plane API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New creates a Client for the given base URL and optional JWT token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetHTTPClient overrides the default HTTP client (used in tests).
func (c *Client) SetHTTPClient(httpClient *http.Client) {
	c.http = httpClient
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	reqURL, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return nil, fmt.Errorf("build url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		req.Header[k] = v
	}
	return c.http.Do(req)
}

// Do performs an HTTP request and returns the raw response.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	return c.request(ctx, method, path, body, headers)
}

// Get performs a JSON GET request and decodes the response into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	resp, err := c.request(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeJSON(resp, out)
}

// Post performs a JSON POST request and decodes the response into out.
func (c *Client) Post(ctx context.Context, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode payload: %w", err)
		}
		body = bytes.NewReader(data)
	}
	resp, err := c.request(ctx, http.MethodPost, path, body, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeJSON(resp, out)
}

// Patch performs a JSON PATCH request and decodes the response into out.
func (c *Client) Patch(ctx context.Context, path string, payload, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	resp, err := c.request(ctx, http.MethodPatch, path, bytes.NewReader(data), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeJSON(resp, out)
}

// Delete performs a DELETE request.
func (c *Client) Delete(ctx context.Context, path string) error {
	resp, err := c.request(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp)
}

func decodeJSON(resp *http.Response, out any) error {
	if err := checkStatus(resp); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
}
