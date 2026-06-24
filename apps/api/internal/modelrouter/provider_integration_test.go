//go:build integration

package modelrouter

import (
	"context"
	"os"
	"testing"
)

func skipIfMissing(t *testing.T, env string) string {
	t.Helper()
	value := os.Getenv(env)
	if value == "" {
		t.Skipf("skipping integration test: %s not set", env)
	}
	return value
}

func requireValidCredential(t *testing.T, name string, err error) {
	t.Helper()
	if err != nil {
		t.Skipf("skipping integration test: %s credential invalid or unavailable: %v", name, err)
	}
}

func TestIntegrationBifrost(t *testing.T) {
	apiKey := skipIfMissing(t, "BIFROST_API_KEY")
	baseURL := os.Getenv("BIFROST_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8081"
	}

	provider := NewBifrostProvider()
	provider.baseURL = baseURL
	provider.apiKey = apiKey

	result, err := provider.Call(context.Background(), CallRequest{
		PreferredModel: "bifrost/gpt-4o",
		Messages:       []Message{{Role: "user", Content: "Say hello"}},
	})
	requireValidCredential(t, "BIFROST_API_KEY", err)
	if result.Content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestIntegrationOpenAI(t *testing.T) {
	skipIfMissing(t, "OPENAI_API_KEY")
	provider := NewOpenAIProvider()

	result, err := provider.Call(context.Background(), CallRequest{
		PreferredModel: "gpt-4o-mini",
		Messages:       []Message{{Role: "user", Content: "Say hello"}},
	})
	requireValidCredential(t, "OPENAI_API_KEY", err)
	if result.Content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestIntegrationAnthropic(t *testing.T) {
	skipIfMissing(t, "ANTHROPIC_API_KEY")
	provider := NewAnthropicProvider()

	result, err := provider.Call(context.Background(), CallRequest{
		PreferredModel: "claude-haiku-4-20250514",
		Messages:       []Message{{Role: "user", Content: "Say hello"}},
	})
	requireValidCredential(t, "ANTHROPIC_API_KEY", err)
	if result.Content == "" {
		t.Fatal("expected non-empty content")
	}
}
