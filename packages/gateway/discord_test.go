package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscordGatewayValidateWithBotToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/@me" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bot test-discord-token" {
			t.Fatalf("authorization = %q", auth)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "bot-1", "username": "bot"})
	}))
	defer server.Close()

	g := NewDiscordGateway("test-discord-token", "")
	g.SetHTTPClient(server.Client())
	g.baseURL = server.URL

	if err := g.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestDiscordGatewayValidateWithWebhook(t *testing.T) {
	g := NewDiscordGateway("", "https://discord.com/api/webhooks/123/token")
	if err := g.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestDiscordGatewaySendMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/channels/channel-1/messages" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if payload["content"] != "hello" {
			t.Fatalf("content = %v", payload["content"])
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	g := NewDiscordGateway("test-discord-token", "")
	g.SetHTTPClient(server.Client())
	g.baseURL = server.URL

	if err := g.SendMessage(context.Background(), "channel-1", "hello"); err != nil {
		t.Fatalf("SendMessage() error: %v", err)
	}
}

func TestDiscordGatewaySendWebhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhook" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if payload["content"] != "hello" {
			t.Fatalf("content = %v", payload["content"])
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	g := NewDiscordGateway("", server.URL+"/webhook")
	g.SetHTTPClient(server.Client())

	if err := g.SendWebhook(context.Background(), "hello"); err != nil {
		t.Fatalf("SendWebhook() error: %v", err)
	}
}
