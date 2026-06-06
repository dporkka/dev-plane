package modelrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIProviderCallUsesChatCompletions(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-openai-key" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"content": `{"action":"final_response","content":"done"}`},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     100,
				"completion_tokens": 20,
				"total_tokens":      120,
			},
		})
	}))
	defer server.Close()

	provider := NewOpenAIProvider()
	provider.baseURL = server.URL
	provider.client = server.Client()

	result, err := provider.Call(context.Background(), CallRequest{
		PreferredModel: "gpt-4o-mini",
		StructuredReq:  true,
		Messages:       []Message{{Role: "user", Content: "return json"}},
	})
	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	if captured["model"] != "gpt-4o-mini" {
		t.Fatalf("model = %v", captured["model"])
	}
	responseFormat, ok := captured["response_format"].(map[string]any)
	if !ok || responseFormat["type"] != "json_object" {
		t.Fatalf("response_format = %#v", captured["response_format"])
	}
	if result.Content != `{"action":"final_response","content":"done"}` {
		t.Fatalf("content = %q", result.Content)
	}
	if result.PromptTokens != 100 || result.CompletionTokens != 20 || result.TotalTokens != 120 {
		t.Fatalf("usage = %d/%d/%d", result.PromptTokens, result.CompletionTokens, result.TotalTokens)
	}
	if result.Provider != "openai" || result.Model != "gpt-4o-mini" {
		t.Fatalf("route = %s/%s", result.Provider, result.Model)
	}
	if result.Cost <= 0 {
		t.Fatalf("cost = %f, want positive estimated cost", result.Cost)
	}
}

func TestAnthropicProviderCallUsesMessagesAPI(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("path = %q, want /messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-anthropic-key" {
			t.Fatalf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("anthropic-version = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "handoff"}},
			"usage": map[string]any{
				"input_tokens":  50,
				"output_tokens": 10,
			},
			"stop_reason": "end_turn",
		})
	}))
	defer server.Close()

	provider := NewAnthropicProvider()
	provider.baseURL = server.URL
	provider.client = server.Client()

	result, err := provider.Call(context.Background(), CallRequest{
		PreferredModel: "claude-haiku-4-20250514",
		Messages: []Message{
			{Role: "system", Content: "system instructions"},
			{Role: "user", Content: "next action"},
		},
	})
	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	if captured["model"] != "claude-haiku-4-20250514" {
		t.Fatalf("model = %v", captured["model"])
	}
	if captured["system"] != "system instructions" {
		t.Fatalf("system = %v", captured["system"])
	}
	if result.Content != "handoff" || result.FinishReason != "end_turn" {
		t.Fatalf("result = %#v", result)
	}
	if result.PromptTokens != 50 || result.CompletionTokens != 10 || result.TotalTokens != 60 {
		t.Fatalf("usage = %d/%d/%d", result.PromptTokens, result.CompletionTokens, result.TotalTokens)
	}
}

func TestGeminiProviderCallUsesGenerateContent(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-gemini-key")
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/gemini-2.5-flash:generateContent" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("key"); got != "test-gemini-key" {
			t.Fatalf("key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"parts": []map[string]any{{"text": "gemini text"}}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":     70,
				"candidatesTokenCount": 15,
				"totalTokenCount":      85,
			},
		})
	}))
	defer server.Close()

	provider := NewGeminiProvider()
	provider.baseURL = server.URL
	provider.client = server.Client()

	result, err := provider.Call(context.Background(), CallRequest{
		PreferredModel: "gemini-2.5-flash",
		StructuredReq:  true,
		Messages: []Message{
			{Role: "system", Content: "system instructions"},
			{Role: "user", Content: "next action"},
		},
	})
	if err != nil {
		t.Fatalf("Call() error: %v", err)
	}
	generationConfig, ok := captured["generationConfig"].(map[string]any)
	if !ok || generationConfig["responseMimeType"] != "application/json" {
		t.Fatalf("generationConfig = %#v", captured["generationConfig"])
	}
	if _, ok := captured["systemInstruction"].(map[string]any); !ok {
		t.Fatalf("systemInstruction = %#v", captured["systemInstruction"])
	}
	if result.Content != "gemini text" || result.FinishReason != "STOP" {
		t.Fatalf("result = %#v", result)
	}
	if result.PromptTokens != 70 || result.CompletionTokens != 15 || result.TotalTokens != 85 {
		t.Fatalf("usage = %d/%d/%d", result.PromptTokens, result.CompletionTokens, result.TotalTokens)
	}
}

func TestOpenAICompatibleProviderCallReturnsHTTPErrorBody(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "test-groq-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
	}))
	defer server.Close()

	provider := NewGroqProvider()
	provider.baseURL = server.URL
	provider.client = server.Client()

	_, err := provider.Call(context.Background(), CallRequest{
		PreferredModel: "llama-4-scout",
		Messages:       []Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("error = %v, want response body", err)
	}
}
