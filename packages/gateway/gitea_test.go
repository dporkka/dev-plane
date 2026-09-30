package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGiteaCreatePullRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/owner/repo/pulls" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "token tea_test" {
			t.Fatalf("authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["title"] != "WIP: agent change" {
			t.Fatalf("title = %q", payload["title"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GiteaPR{
			Number: 7, HTMLURL: serverURL(r) + "/owner/repo/pulls/7", State: "open", Draft: true,
		})
	}))
	defer server.Close()

	gateway := NewGiteaGateway(server.URL)
	pr, err := gateway.CreatePullRequest(context.Background(), "tea_test", "owner", "repo", NewPR{
		Title: "agent change", Body: "body", Head: "agent/task", Base: "main", Draft: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || !pr.Draft {
		t.Fatalf("unexpected pr: %+v", pr)
	}
}

func TestGiteaMergePullRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/owner/repo/pulls/7/merge" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["do"] != "squash" || payload["head_commit_id"] != "abc123" {
			t.Fatalf("payload = %#v", payload)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	gateway := NewGiteaGateway(server.URL)
	result, err := gateway.MergePullRequest(context.Background(), "tea_test", "owner", "repo", 7, MergePRRequest{
		Method: "squash", SHA: "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Merged || result.SHA != "abc123" {
		t.Fatalf("unexpected merge result: %+v", result)
	}
}

func TestGiteaAPIErrorIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat("x", 5000), http.StatusUnauthorized)
	}))
	defer server.Close()

	gateway := NewGiteaGateway(server.URL)
	_, err := gateway.GetRepo(context.Background(), "bad", "owner", "repo")
	if err == nil || !strings.Contains(err.Error(), "gitea API error 401") {
		t.Fatalf("error = %v", err)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
