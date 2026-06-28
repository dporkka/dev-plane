package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestConfig(t *testing.T, baseURL string) func() {
	t.Helper()
	tmp := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	cfg := map[string]string{"base_url": baseURL, "token": "test-token"}
	data, _ := json.Marshal(cfg)
	dir := filepath.Join(tmp, ".config", "dev-plane")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return func() { os.Setenv("HOME", originalHome) }
}

func TestTasksList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects/p1/tasks" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]Task{{ID: "t1", Title: "Task One", Status: "backlog", Priority: "high"}})
	}))
	defer server.Close()
	cleanup := setupTestConfig(t, server.URL)
	defer cleanup()

	if err := run([]string{"tasks", "list", "--project-id", "p1"}); err != nil {
		t.Fatalf("tasks list: %v", err)
	}
}

func TestTasksGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks/t1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Task{ID: "t1", Title: "Task One"})
	}))
	defer server.Close()
	cleanup := setupTestConfig(t, server.URL)
	defer cleanup()

	if err := run([]string{"tasks", "get", "t1"}); err != nil {
		t.Fatalf("tasks get: %v", err)
	}
}

func TestApprovalsRespond(t *testing.T) {
	var gotResponse string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/approvals/a1/respond" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var payload RespondApprovalRequest
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotResponse = payload.Response
		_ = json.NewEncoder(w).Encode(Approval{ID: "a1", Response: payload.Response})
	}))
	defer server.Close()
	cleanup := setupTestConfig(t, server.URL)
	defer cleanup()

	if err := run([]string{"approvals", "respond", "--response", "approved", "a1"}); err != nil {
		t.Fatalf("approvals respond: %v", err)
	}
	if gotResponse != "approved" {
		t.Fatalf("expected approved, got %q", gotResponse)
	}
}

func TestRunsLogsSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/runs/r1/stream" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if accept := r.Header.Get("Accept"); accept != "text/event-stream" {
			t.Errorf("expected text/event-stream, got %q", accept)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"run_id\":\"r1\",\"status\":\"running\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"run_id\":\"r1\",\"status\":\"completed\"}\n\n"))
	}))
	defer server.Close()
	cleanup := setupTestConfig(t, server.URL)
	defer cleanup()

	if err := run([]string{"runs", "logs", "r1"}); err != nil {
		t.Fatalf("runs logs: %v", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	err := run([]string{"unknown"})
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}
