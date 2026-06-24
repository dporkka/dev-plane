package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DiscordGateway provides methods for interacting with Discord.
type DiscordGateway struct {
	botToken   string
	webhookURL string
	baseURL    string
	httpClient *http.Client
}

// NewDiscordGateway creates a new Discord gateway.
// Either botToken or webhookURL may be empty depending on the intended use.
func NewDiscordGateway(botToken, webhookURL string) *DiscordGateway {
	return &DiscordGateway{
		botToken:   botToken,
		webhookURL: webhookURL,
		baseURL:    "https://discord.com/api/v10",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetHTTPClient overrides the default HTTP client (useful for tests).
func (g *DiscordGateway) SetHTTPClient(client *http.Client) {
	g.httpClient = client
}

// Validate checks that the bot token or webhook URL is usable.
func (g *DiscordGateway) Validate(ctx context.Context) error {
	if g.webhookURL != "" {
		// Webhook URLs cannot be validated without posting; assume valid if present.
		return nil
	}
	if g.botToken == "" {
		return fmt.Errorf("discord bot token or webhook url is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+"/users/@me", nil)
	if err != nil {
		return fmt.Errorf("create discord request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+g.botToken)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("discord request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord validation returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// SendMessage sends a message to a Discord channel using the bot token.
func (g *DiscordGateway) SendMessage(ctx context.Context, channelID, content string) error {
	if g.botToken == "" {
		return fmt.Errorf("discord bot token is required")
	}
	if channelID == "" {
		return fmt.Errorf("discord channel id is required")
	}
	if content == "" {
		return fmt.Errorf("discord message content is required")
	}

	body := map[string]any{"content": content}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal discord message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/channels/"+channelID+"/messages", bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("create discord message request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+g.botToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("discord message request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord message returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// SendWebhook sends a message via a Discord webhook URL.
func (g *DiscordGateway) SendWebhook(ctx context.Context, content string) error {
	if g.webhookURL == "" {
		return fmt.Errorf("discord webhook url is required")
	}
	if content == "" {
		return fmt.Errorf("discord webhook content is required")
	}

	body := map[string]any{"content": content}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal discord webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.webhookURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("create discord webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("discord webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord webhook returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
