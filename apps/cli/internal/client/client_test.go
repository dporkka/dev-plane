package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("expected bearer token, got %q", auth)
		}
		if r.URL.Path == "/api/v1/tasks/missing" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "task-1", "title": "Hello"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := New(server.URL, "test-token")
	var out map[string]string
	if err := c.Get(context.Background(), "/api/v1/tasks/task-1", &out); err != nil {
		t.Fatalf("get task: %v", err)
	}
	if out["id"] != "task-1" {
		t.Fatalf("unexpected response: %+v", out)
	}

	var errOut map[string]string
	err := c.Get(context.Background(), "/api/v1/tasks/missing", &errOut)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}

func TestPost(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/projects/{id}/tasks", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"title":"Test"`) {
			t.Errorf("unexpected body: %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "new-task"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := New(server.URL, "")
	var out map[string]string
	if err := c.Post(context.Background(), "/api/v1/projects/p1/tasks", map[string]string{"title": "Test"}, &out); err != nil {
		t.Fatalf("post task: %v", err)
	}
	if out["id"] != "new-task" {
		t.Fatalf("unexpected response: %+v", out)
	}
}
