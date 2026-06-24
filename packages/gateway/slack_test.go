package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSlackGatewayValidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth.test" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-slack-token" {
			t.Fatalf("authorization = %q", auth)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "team": "Acme", "user": "bot"})
	}))
	defer server.Close()

	g := NewSlackGateway("test-slack-token")
	g.SetHTTPClient(server.Client())
	g.baseURL = server.URL

	if err := g.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestSlackGatewayValidateError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
	}))
	defer server.Close()

	g := NewSlackGateway("bad-token")
	g.SetHTTPClient(server.Client())
	g.baseURL = server.URL

	if err := g.Validate(context.Background()); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSlackGatewayPostMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse body: %v", err)
		}
		if values.Get("channel") != "#alerts" {
			t.Fatalf("channel = %q", values.Get("channel"))
		}
		if values.Get("text") != "hello" {
			t.Fatalf("text = %q", values.Get("text"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	g := NewSlackGateway("test-slack-token")
	g.SetHTTPClient(server.Client())
	g.baseURL = server.URL

	if err := g.PostMessage(context.Background(), "#alerts", "hello"); err != nil {
		t.Fatalf("PostMessage() error: %v", err)
	}
}
