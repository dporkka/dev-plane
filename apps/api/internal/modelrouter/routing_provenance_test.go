package modelrouter

import (
	"context"
	"testing"
)

func TestBifrostRouteMarksGatewaySpendAuthority(t *testing.T) {
	bifrost := &mockProvider{
		name:      "bifrost",
		available: true,
		models: []ModelInfo{
			{Name: "bifrost/default", Provider: "bifrost", MaxContext: 128000, CodingStrength: 8, ReasoningStrength: 8, CostPer1KOutput: 0.01},
		},
	}
	router := NewRouter(DefaultConfig(), bifrost)

	result, err := router.RouteCall(context.Background(), CallRequest{
		Route:       RouteCodingDeep,
		TaskType:    TaskTypeCode,
		Difficulty:  DifficultyHard,
		LatencyReq:  LatencyNormal,
		Messages:    []Message{{Role: "user", Content: "Implement the fix"}},
	})
	if err != nil {
		t.Fatalf("RouteCall() error: %v", err)
	}
	if result.RouteSource != RouteSourceExplicit {
		t.Fatalf("route source = %q, want %q", result.RouteSource, RouteSourceExplicit)
	}
	if result.PolicyVersion != SemanticRoutingPolicyVersion {
		t.Fatalf("policy version = %q, want %q", result.PolicyVersion, SemanticRoutingPolicyVersion)
	}
	if result.SpendAuthority != SpendAuthorityGateway {
		t.Fatalf("spend authority = %q, want %q", result.SpendAuthority, SpendAuthorityGateway)
	}
	if !router.UsesExternalSpendAuthority() {
		t.Fatal("expected router to report external spend authority")
	}
}

func TestLegacyRouteMarksDevPlaneSpendAuthority(t *testing.T) {
	direct := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{Name: "direct-model", Provider: "openai", MaxContext: 128000, CodingStrength: 9, ReasoningStrength: 9, CostPer1KOutput: 0.01},
		},
	}
	router := NewRouter(&Config{
		DefaultModel:     "direct-model",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1,
		ProviderPriority: []string{"openai"},
	}, direct)

	result, err := router.RouteCall(context.Background(), CallRequest{
		Route:       RouteAuto,
		TaskType:    TaskTypeCode,
		Difficulty:  DifficultyMedium,
		LatencyReq:  LatencyNormal,
		Messages:    []Message{{Role: "user", Content: "Implement the handler"}},
	})
	if err != nil {
		t.Fatalf("RouteCall() error: %v", err)
	}
	if result.RouteSource != RouteSourceLegacyLocal {
		t.Fatalf("route source = %q, want %q", result.RouteSource, RouteSourceLegacyLocal)
	}
	if result.SpendAuthority != SpendAuthorityDevPlane {
		t.Fatalf("spend authority = %q, want %q", result.SpendAuthority, SpendAuthorityDevPlane)
	}
	if router.UsesExternalSpendAuthority() {
		t.Fatal("legacy direct router must not report external spend authority")
	}
}
