package modelrouter

import (
	"context"
	"testing"
)

func TestRouteCallUsesBifrostAutoWhenGatewayIsAvailable(t *testing.T) {
	direct := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{Name: "direct-model", Provider: "openai", MaxContext: 128000, CodingStrength: 10, ReasoningStrength: 10, CostPer1KOutput: 0.01},
		},
	}
	bifrost := &mockProvider{
		name:      "bifrost",
		available: true,
		models: []ModelInfo{
			{Name: "bifrost/default", Provider: "bifrost", MaxContext: 128000, CodingStrength: 1, ReasoningStrength: 1, CostPer1KOutput: 0.01},
		},
	}

	router := NewRouter(&Config{
		DefaultModel:     "direct-model",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1,
		ProviderPriority: []string{"openai", "bifrost"},
	}, direct, bifrost)

	result, err := router.RouteCall(context.Background(), CallRequest{
		TaskType:    TaskTypeDebug,
		Difficulty:  DifficultyHard,
		LatencyReq:  LatencyNormal,
		Messages:    []Message{{Role: "user", Content: "Find the deadlock"}},
	})
	if err != nil {
		t.Fatalf("RouteCall() error: %v", err)
	}
	if got, want := bifrost.lastReq.PreferredModel, "route/auto"; got != want {
		t.Fatalf("bifrost model alias = %q, want %q", got, want)
	}
	if got, want := result.Route, RouteAuto; got != want {
		t.Fatalf("result route = %q, want %q", got, want)
	}
	if direct.lastReq.PreferredModel != "" {
		t.Fatalf("direct provider should not be called when Bifrost is available, got %q", direct.lastReq.PreferredModel)
	}
}
