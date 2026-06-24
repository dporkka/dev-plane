package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SlackGateway provides methods for interacting with the Slack API.
type SlackGateway struct {
	botToken   string
	baseURL    string
	httpClient *http.Client
}

// SlackAuthTestResponse is the response from Slack auth.test.
type SlackAuthTestResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Team  string `json:"team,omitempty"`
	User  string `json:"user,omitempty"`
}

// NewSlackGateway creates a new Slack gateway.
func NewSlackGateway(botToken string) *SlackGateway {
	return &SlackGateway{
		botToken:   botToken,
		baseURL:    "https://slack.com/api",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetHTTPClient overrides the default HTTP client (useful for tests).
func (g *SlackGateway) SetHTTPClient(client *http.Client) {
	g.httpClient = client
}

// Validate checks that the bot token is valid.
func (g *SlackGateway) Validate(ctx context.Context) error {
	var result SlackAuthTestResponse
	if err := g.postForm(ctx, "/auth.test", url.Values{}, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("slack validation failed: %s", result.Error)
	}
	return nil
}

// PostMessage sends a message to a Slack channel.
func (g *SlackGateway) PostMessage(ctx context.Context, channel, text string) error {
	if channel == "" {
		return fmt.Errorf("slack channel is required")
	}
	if text == "" {
		return fmt.Errorf("slack message text is required")
	}

	values := url.Values{
		"channel": {channel},
		"text":    {text},
	}
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := g.postForm(ctx, "/chat.postMessage", values, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("slack post message failed: %s", result.Error)
	}
	return nil
}

func (g *SlackGateway) postForm(ctx context.Context, path string, values url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+path, strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("create slack request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.botToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack request returned %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode slack response: %w", err)
	}
	return nil
}
