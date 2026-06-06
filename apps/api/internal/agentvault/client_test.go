package agentvault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientLogEventPostsCapture(t *testing.T) {
	var gotToken string
	var gotPayload map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/capture" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotToken = r.Header.Get("X-AgentVault-Token")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"path":"00-inbox/event.md"}`))
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "token-123")
	if client == nil {
		t.Fatal("expected client")
	}

	err := client.LogEvent(t.Context(), Event{
		Type:    "dev-plane.task.created",
		Title:   "Task created",
		Text:    "Task details",
		Project: "dev-plane",
		Tags:    []string{"dev-plane", "task"},
	})
	if err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	if gotToken != "token-123" {
		t.Errorf("token = %q, want token-123", gotToken)
	}
	if gotPayload["title"] != "Task created" {
		t.Errorf("title = %v", gotPayload["title"])
	}
	if gotPayload["project"] != "dev-plane" {
		t.Errorf("project = %v", gotPayload["project"])
	}
	if gotPayload["type"] != "dev-plane.task.created" {
		t.Errorf("type = %v", gotPayload["type"])
	}
}

func TestNewClientDisabledWhenMissingConfig(t *testing.T) {
	if got := NewClient("", "token"); got != nil {
		t.Fatal("expected nil client without URL")
	}
	if got := NewClient("http://localhost:47321", ""); got != nil {
		t.Fatal("expected nil client without token")
	}
}
