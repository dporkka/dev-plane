package agentvault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/activity"
)

// Event remains as a compatibility alias for callers that used the original
// AgentVault-specific event type before lifecycle events became provider-neutral.
type Event = activity.Event

// Client writes Dev Plane lifecycle events to AgentVault's local HTTP API.
type Client struct {
	baseURL    string
	authToken  string
	httpClient *http.Client
}

// NewClient creates an AgentVault client. Empty URLs or tokens disable logging.
func NewClient(baseURL, authToken string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	authToken = strings.TrimSpace(authToken)
	if baseURL == "" || authToken == "" {
		return nil
	}
	return &Client{
		baseURL:   baseURL,
		authToken: authToken,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

// Publish implements activity.Sink by capturing an event as an AgentVault inbox note.
func (c *Client) Publish(ctx context.Context, event activity.Event) error {
	if c == nil {
		return nil
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.Type == "" {
		event.Type = "dev-plane.event"
	}
	if len(event.Tags) == 0 {
		event.Tags = []string{"dev-plane"}
	}
	payload := map[string]any{
		"type":        event.Type,
		"title":       event.Title,
		"text":        renderEventText(event),
		"project":     event.Project,
		"tags":        event.Tags,
		"external_id": event.ExternalID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal agentvault event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/capture", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create agentvault request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentVault-Token", c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post agentvault capture: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("agentvault capture returned status %d", resp.StatusCode)
	}
	return nil
}

// LogEvent is retained for backward compatibility with the original adapter API.
func (c *Client) LogEvent(ctx context.Context, event Event) error {
	return c.Publish(ctx, event)
}

func renderEventText(event activity.Event) string {
	var b strings.Builder
	b.WriteString(event.Text)
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString("## Event Metadata\n\n")
	b.WriteString(fmt.Sprintf("- Type: %s\n", event.Type))
	b.WriteString(fmt.Sprintf("- Created: %s\n", event.CreatedAt.UTC().Format(time.RFC3339)))
	if event.ExternalID != "" {
		b.WriteString(fmt.Sprintf("- External ID: %s\n", event.ExternalID))
	}
	for key, value := range event.Metadata {
		b.WriteString(fmt.Sprintf("- %s: %v\n", key, value))
	}
	return b.String()
}

var _ activity.Sink = (*Client)(nil)
