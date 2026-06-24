package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LinearGateway provides methods for interacting with the Linear API.
type LinearGateway struct {
	apiKey   string
	baseURL  string
	httpClient *http.Client
}

// LinearIssue represents a created Linear issue.
type LinearIssue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	State       string `json:"state"`
}

// LinearTeam represents a Linear team.
type LinearTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

// NewLinearGateway creates a new Linear gateway.
func NewLinearGateway(apiKey string) *LinearGateway {
	return &LinearGateway{
		apiKey:   apiKey,
		baseURL:  "https://api.linear.app",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetHTTPClient overrides the default HTTP client (useful for tests).
func (g *LinearGateway) SetHTTPClient(client *http.Client) {
	g.httpClient = client
}

// Validate checks that the configured API key can access Linear.
func (g *LinearGateway) Validate(ctx context.Context) error {
	query := `{"query": "query { viewer { id name } }"}`
	var result struct {
		Data struct {
			Viewer struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := g.postGraphQL(ctx, query, &result); err != nil {
		return err
	}
	if len(result.Errors) > 0 {
		return fmt.Errorf("linear validation failed: %s", result.Errors[0].Message)
	}
	if result.Data.Viewer.ID == "" {
		return fmt.Errorf("linear validation failed: no viewer returned")
	}
	return nil
}

// GetTeams lists teams accessible to the API key.
func (g *LinearGateway) GetTeams(ctx context.Context) ([]LinearTeam, error) {
	query := `{"query": "query { teams { nodes { id name key } } }"}`
	var result struct {
		Data struct {
			Teams struct {
				Nodes []LinearTeam `json:"nodes"`
			} `json:"teams"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := g.postGraphQL(ctx, query, &result); err != nil {
		return nil, err
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("linear get teams failed: %s", result.Errors[0].Message)
	}
	return result.Data.Teams.Nodes, nil
}

// CreateIssue creates a new Linear issue in the given team.
func (g *LinearGateway) CreateIssue(ctx context.Context, teamID, title, description string) (*LinearIssue, error) {
	if teamID == "" {
		return nil, fmt.Errorf("linear team id is required")
	}
	if title == "" {
		return nil, fmt.Errorf("linear issue title is required")
	}

	input := map[string]any{
		"teamId":      teamID,
		"title":       title,
		"description": description,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal issue input: %w", err)
	}
	query := fmt.Sprintf(`{"query": "mutation IssueCreate($input: IssueCreateInput!) { issueCreate(input: $input) { issue { id identifier title url state { name } } } }", "variables": {"input": %s}}`, string(inputJSON))

	var result struct {
		Data struct {
			IssueCreate struct {
				Issue struct {
					ID         string `json:"id"`
					Identifier string `json:"identifier"`
					Title      string `json:"title"`
					URL        string `json:"url"`
					State      struct {
						Name string `json:"name"`
					} `json:"state"`
				} `json:"issue"`
			} `json:"issueCreate"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := g.postGraphQL(ctx, query, &result); err != nil {
		return nil, err
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("linear create issue failed: %s", result.Errors[0].Message)
	}
	issue := result.Data.IssueCreate.Issue
	return &LinearIssue{
		ID:         issue.ID,
		Identifier: issue.Identifier,
		Title:      issue.Title,
		URL:        issue.URL,
		State:      issue.State.Name,
	}, nil
}

func (g *LinearGateway) postGraphQL(ctx context.Context, query string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/graphql", strings.NewReader(query))
	if err != nil {
		return fmt.Errorf("create linear request: %w", err)
	}
	req.Header.Set("Authorization", g.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("linear request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("linear request returned %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode linear response: %w", err)
	}
	return nil
}
