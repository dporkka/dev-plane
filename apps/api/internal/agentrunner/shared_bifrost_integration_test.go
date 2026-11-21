package agentrunner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/modelrouter"
)

func TestDefaultAgentRunnerCanRouteStructuredActionViaSharedBifrost(t *testing.T) {
	t.Setenv("BIFROST_SHARED_ROUTING_ENABLED", "true")
	t.Setenv("BIFROST_API_KEY", "test-devplane-virtual-key")
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path=%q", r.URL.Path)
		}
		if r.Header.Get("X-Bf-Vk") != "test-devplane-virtual-key" {
			t.Error("missing product virtual key")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("virtual key sent as Bearer")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "bifrost/code-review" {
			t.Errorf("model=%v", body["model"])
		}
		responseFormat, ok := body["response_format"].(map[string]any)
		if !ok || responseFormat["type"] != "json_object" {
			t.Errorf("missing structured response contract: %#v", body["response_format"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "openrouter/openai/gpt-6-luna",
			"choices": []any{map[string]any{"message": map[string]any{"content": "{\"action\":\"final_response\",\"content\":\"OK\"}"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 23, "completion_tokens": 8},
		})
	}))
	defer server.Close()
	t.Setenv("BIFROST_URL", server.URL)
	runner := NewRunner(nil, nil, nil, nil, nil, nil)
	got, err := runner.router.RouteCall(context.Background(), modelrouter.CallRequest{
		TaskType: modelrouter.TaskTypeReview, StructuredReq: true, ContextSize: 4096,
		Messages: []modelrouter.Message{{Role: "user", Content: "Review this safely"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("production runner did not send the request")
	}
	if got.Model != "openrouter/openai/gpt-6-luna" || got.Provider != "bifrost" {
		t.Fatalf("model/provider mismatch: %#v", got)
	}
	if got.Content != "{\"action\":\"final_response\",\"content\":\"OK\"}" {
		t.Fatalf("model action=%q", got.Content)
	}
}
