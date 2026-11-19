package modelrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Models are configured explicitly, not guessed from the names of commercial APIs.
const verifiedBifrostModel = `[{"id":"groq/openai/gpt-oss-120b","max_context":32768,"coding_strength":8,"reasoning_strength":8,"latency_ms":500,"input_usd_per_1k":0,"output_usd_per_1k":0,"pricing_verified":true,"supports_structured_output":false,"supports_vision":false,"supports_function_calling":false}]`

func TestBifrostCatalogRequiresVerifiedExplicitModelDescriptors(t *testing.T) {
	cases := []struct{ name, catalog string }{
		{"missing", ""},
		{"malformed", `[{"id":`},
		{"unqualified", `[{"id":"gpt-oss-120b","max_context":32768,"pricing_verified":true}]`},
		{"unknown_provider", `[{"id":"bifrost/gpt-4o","max_context":32768,"pricing_verified":true}]`},
		{"missing_price_verification", `[{"id":"groq/openai/gpt-oss-120b","max_context":32768}]`},
		{"unknown_context", `[{"id":"groq/openai/gpt-oss-120b","pricing_verified":true}]`},
		{"negative_price", `[{"id":"groq/openai/gpt-oss-120b","max_context":32768,"pricing_verified":true,"input_usd_per_1k":-1}]`},
		{"duplicate", `[{"id":"groq/openai/gpt-oss-120b","max_context":32768,"pricing_verified":true},{"id":"groq/openai/gpt-oss-120b","max_context":32768,"pricing_verified":true}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BIFROST_API_KEY", "fake-test-only")
			t.Setenv("BIFROST_MODEL_CATALOG_JSON", tc.catalog)
			p := NewBifrostProvider()
			if m := p.Models(); len(m) != 0 {
				t.Fatalf("unverified catalog exposed models: %+v", m)
			}
			if p.IsAvailable() {
				t.Fatal("invalid or empty catalog must disable Bifrost routing")
			}
		})
	}
}

func TestBifrostCatalogCarriesProviderQualifiedModelsWithoutInventedPrices(t *testing.T) {
	t.Setenv("BIFROST_API_KEY", "fake-test-only")
	t.Setenv("BIFROST_MODEL_CATALOG_JSON", verifiedBifrostModel)
	p := NewBifrostProvider()
	if !p.IsAvailable() {
		t.Fatal("explicitly configured gateway should be eligible")
	}
	models := p.Models()
	if len(models) != 1 {
		t.Fatalf("expected exactly one model, got %+v", models)
	}
	if models[0].Name != "groq/openai/gpt-oss-120b" || models[0].Provider != "bifrost" {
		t.Fatalf("unexpected model %+v", models[0])
	}
	if models[0].MaxContext != 32768 || models[0].CostPer1KOutput != 0 {
		t.Fatalf("operator metadata lost %+v", models[0])
	}
}

func TestBifrostProviderUsesCanonicalV1AndRejectsModelSubstitution(t *testing.T) {
	t.Setenv("BIFROST_API_KEY", "fake-test-only")
	t.Setenv("BIFROST_MODEL_CATALOG_JSON", verifiedBifrostModel)
	var forwarded string
	var actualModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("invalid Bifrost path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fake-test-only" {
			t.Error("Bifrost bearer token missing")
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		forwarded, _ = req["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   actualModel,
			"choices": []map[string]any{{"message": map[string]string{"content": "{}"}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
		})
	}))
	defer server.Close()
	t.Setenv("BIFROST_URL", server.URL+"/v1")
	p := NewBifrostProvider()
	actualModel = "openai/gpt-oss-120b"
	res, err := p.Call(context.Background(), CallRequest{PreferredModel: "groq/openai/gpt-oss-120b", Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatalf("verified model rejected: %v", err)
	}
	if forwarded != "groq/openai/gpt-oss-120b" {
		t.Fatalf("incorrect forwarded model %q", forwarded)
	}
	if res.Model != "groq/openai/gpt-oss-120b" {
		t.Fatalf("model response %q", res.Model)
	}
	actualModel = "other/paid-model"
	_, err = p.Call(context.Background(), CallRequest{PreferredModel: "groq/openai/gpt-oss-120b", Messages: []Message{{Role: "user", Content: "hello"}}})
	if err == nil || !strings.Contains(err.Error(), "model mismatch") {
		t.Fatalf("must reject model substitution, got %v", err)
	}
}

func TestBifrostProviderRejectsUnlistedModelsBeforeHTTP(t *testing.T) {
	t.Setenv("BIFROST_API_KEY", "fake-test-only")
	t.Setenv("BIFROST_MODEL_CATALOG_JSON", verifiedBifrostModel)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	t.Setenv("BIFROST_URL", server.URL+"/v1")
	provider := NewBifrostProvider()
	_, err := provider.Call(context.Background(), CallRequest{PreferredModel: "openrouter/expensive-unlisted", Messages: []Message{{Role: "user", Content: "hello"}}})
	if err == nil || requests != 0 {
		t.Fatalf("unlisted model reached upstream; requests=%d err=%v", requests, err)
	}
}

func TestBifrostDefaultBaseURLIncludesGatewayV1Path(t *testing.T) {
	t.Setenv("BIFROST_URL", "")
	p := NewBifrostProvider()
	if p.baseURL != "http://localhost:3000/v1" {
		t.Fatalf("gateway default points to %q", p.baseURL)
	}
}

func TestRouterInitialSelectionRejectsHighInputPriceEvenWhenOutputIsCheap(t *testing.T) {
	expensive := codingModel("high-input-cost", "expensive", 10)
	expensive.CostPer1KInput = 0.5
	expensive.CostPer1KOutput = 0.001
	safe := codingModel("safe", "safe", 2)
	safe.CostPer1KInput = 0.002
	safe.CostPer1KOutput = 0.002

	router := NewRouter(&Config{DefaultModel: "unsafe", DefaultProvider: "expensive", MaxCostPer1K: 1},
		&mockProvider{name: "expensive", available: true, models: []ModelInfo{expensive}},
		&mockProvider{name: "safe", available: true, models: []ModelInfo{safe}})
	got, err := router.SelectModel(context.Background(), TaskTypeCode, DifficultyMedium, LatencyNormal, 1000, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "safe" {
		t.Fatalf("over-budget input price bypassed hard cap: %s", got.Name)
	}
}

func TestRouterInitialSelectionRequiresStructuredSupportWhenRequested(t *testing.T) {
	unsupported := codingModel("unsupported", "unsupported", 10)
	unsupported.SupportsStructuredOutput = false
	supported := codingModel("supported", "supported", 3)

	router := NewRouter(&Config{DefaultModel: "unsupported", DefaultProvider: "unsupported", MaxCostPer1K: 1},
		&mockProvider{name: "unsupported", available: true, models: []ModelInfo{unsupported}},
		&mockProvider{name: "supported", available: true, models: []ModelInfo{supported}})
	res, err := router.RouteCall(context.Background(), CallRequest{
		TaskType: TaskTypeCode, Difficulty: DifficultyMedium, LatencyReq: LatencyNormal,
		StructuredReq: true, Messages: []Message{{Role: "user", Content: "return JSON"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "supported" {
		t.Fatalf("structured task was routed to incompatible primary %q", res.Provider)
	}
}

func TestRouterInitialSelectionExcludesUnknownContextCapacity(t *testing.T) {
	unknown := codingModel("unknown", "unknown", 10)
	unknown.MaxContext = 0
	known := codingModel("known", "known", 4)
	known.MaxContext = 8000
	router := NewRouter(&Config{DefaultModel: "unknown", DefaultProvider: "unknown", MaxCostPer1K: 1},
		&mockProvider{name: "unknown", available: true, models: []ModelInfo{unknown}},
		&mockProvider{name: "known", available: true, models: []ModelInfo{known}})
	got, err := router.SelectModel(context.Background(), TaskTypeCode, DifficultyMedium, LatencyNormal, 6000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "known" {
		t.Fatalf("unknown max context accepted: %s", got.Name)
	}
}

func TestRouterInitialSelectionFailsClosedWhenAllModelsExceedExplicitCap(t *testing.T) {
	expensive := codingModel("expensive", "provider", 9)
	expensive.CostPer1KInput = 0.50
	expensive.CostPer1KOutput = 0.50
	router := NewRouter(&Config{DefaultModel: "expensive", DefaultProvider: "provider", MaxCostPer1K: 1},
		&mockProvider{name: "provider", available: true, models: []ModelInfo{expensive}})
	if _, err := router.SelectModel(context.Background(), TaskTypeCode, DifficultyMedium, LatencyNormal, 1000, 0.001); err == nil {
		t.Fatal("hard-cap violation manufactured an ineligible fallback model")
	}
}
