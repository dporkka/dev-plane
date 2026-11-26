package modelrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouteCallSemanticRouteUsesBifrostAlias(t *testing.T) {
	direct := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{Name: "direct-model", Provider: "openai", MaxContext: 128000, CodingStrength: 9, ReasoningStrength: 8, CostPer1KOutput: 0.01},
		},
	}
	bifrost := &mockProvider{
		name:      "bifrost",
		available: true,
		models: []ModelInfo{
			{Name: "bifrost/default", Provider: "bifrost", MaxContext: 128000, CodingStrength: 8, ReasoningStrength: 8, CostPer1KOutput: 0.01},
		},
	}

	router := NewRouter(&Config{
		DefaultModel:     "direct-model",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1,
		ProviderPriority: []string{"openai", "bifrost"},
	}, direct, bifrost)

	result, err := router.RouteCall(context.Background(), CallRequest{
		Route:       RouteCodingDeep,
		TaskType:    TaskTypeCode,
		Difficulty:  DifficultyHard,
		LatencyReq:  LatencyNormal,
		Messages:    []Message{{Role: "user", Content: "Fix a concurrency bug"}},
	})
	if err != nil {
		t.Fatalf("RouteCall() error: %v", err)
	}
	if got, want := bifrost.lastReq.PreferredModel, "route/coding_deep"; got != want {
		t.Fatalf("bifrost model alias = %q, want %q", got, want)
	}
	if direct.lastReq.PreferredModel != "" {
		t.Fatalf("direct provider should not be called for semantic route, got %q", direct.lastReq.PreferredModel)
	}
	if got, want := result.Route, RouteCodingDeep; got != want {
		t.Fatalf("result route = %q, want %q", got, want)
	}
}

func TestRouteCallSemanticRouteFallsBackToLegacySelectionWhenBifrostUnavailable(t *testing.T) {
	direct := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{Name: "direct-model", Provider: "openai", MaxContext: 128000, CodingStrength: 9, ReasoningStrength: 8, CostPer1KOutput: 0.01},
		},
	}
	bifrost := &mockProvider{name: "bifrost", available: false}

	router := NewRouter(&Config{
		DefaultModel:     "direct-model",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1,
		ProviderPriority: []string{"openai", "bifrost"},
	}, direct, bifrost)

	result, err := router.RouteCall(context.Background(), CallRequest{
		Route:       RouteAuto,
		TaskType:    TaskTypeCode,
		Difficulty:  DifficultyMedium,
		LatencyReq:  LatencyNormal,
		Messages:    []Message{{Role: "user", Content: "Implement a handler"}},
	})
	if err != nil {
		t.Fatalf("RouteCall() error: %v", err)
	}
	if got, want := result.Model, "direct-model"; got != want {
		t.Fatalf("model = %q, want %q", got, want)
	}
	if got, want := direct.lastReq.PreferredModel, "direct-model"; got != want {
		t.Fatalf("direct provider model = %q, want %q", got, want)
	}
}

func TestBifrostRoutingHeadersExposeOnlyRoutingContext(t *testing.T) {
	req := CallRequest{
		Route:      RouteAgenticLongHorizon,
		TaskType:   TaskTypeCode,
		Difficulty: DifficultyExpert,
		LatencyReq: LatencySlowOK,
		RoutingMetadata: map[string]string{
			"agent-role": "implementer",
			"risk":       "critical",
			"ignored":    "do-not-forward",
		},
	}

	headers := bifrostRoutingHeaders(req)
	want := map[string]string{
		"x-devplane-route":      RouteAgenticLongHorizon,
		"x-devplane-task-type":  TaskTypeCode,
		"x-devplane-difficulty": DifficultyExpert,
		"x-devplane-latency":    LatencySlowOK,
		"x-devplane-agent-role": "implementer",
		"x-devplane-risk":       "critical",
	}
	for key, value := range want {
		if got := headers[key]; got != value {
			t.Fatalf("header %s = %q, want %q", key, got, value)
		}
	}
	if _, ok := headers["x-devplane-ignored"]; ok {
		t.Fatal("unexpected unapproved routing metadata header")
	}
}

func TestOpenAICompatibleCapturesResolvedModelFromGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"deepseek/deepseek-flash",
			"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}
		}`))
	}))
	defer server.Close()

	result, err := callOpenAICompatible(
		context.Background(),
		server.Client(),
		server.URL,
		"test-key",
		"bifrost",
		CallRequest{PreferredModel: "route/coding_deep", Messages: []Message{{Role: "user", Content: "hello"}}},
		nil,
	)
	if err != nil {
		t.Fatalf("callOpenAICompatible() error: %v", err)
	}
	if got, want := result.Model, "deepseek/deepseek-flash"; got != want {
		t.Fatalf("resolved model = %q, want %q", got, want)
	}
}
