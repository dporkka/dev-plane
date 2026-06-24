package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLinearGatewayValidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Fatalf("path = %q, want /graphql", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "test-linear-key" {
			t.Fatalf("authorization = %q", auth)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"viewer": map[string]any{"id": "viewer-1", "name": "Test User"},
			},
		})
	}))
	defer server.Close()

	g := NewLinearGateway("test-linear-key")
	g.SetHTTPClient(server.Client())
	g.baseURL = server.URL

	if err := g.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestLinearGatewayValidateError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "Invalid API key"}},
		})
	}))
	defer server.Close()

	g := NewLinearGateway("bad-key")
	g.SetHTTPClient(server.Client())
	g.baseURL = server.URL

	if err := g.Validate(context.Background()); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLinearGatewayCreateIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"issueCreate": map[string]any{
					"issue": map[string]any{
						"id":         "issue-1",
						"identifier": "APP-1",
						"title":      "Fix bug",
						"url":        "https://linear.app/issue/APP-1",
						"state":      map[string]any{"name": "Backlog"},
					},
				},
			},
		})
	}))
	defer server.Close()

	g := NewLinearGateway("test-linear-key")
	g.SetHTTPClient(server.Client())
	g.baseURL = server.URL

	issue, err := g.CreateIssue(context.Background(), "team-1", "Fix bug", "bug description")
	if err != nil {
		t.Fatalf("CreateIssue() error: %v", err)
	}
	if issue.ID != "issue-1" {
		t.Fatalf("issue id = %q", issue.ID)
	}
	if issue.Identifier != "APP-1" {
		t.Fatalf("identifier = %q", issue.Identifier)
	}
}
