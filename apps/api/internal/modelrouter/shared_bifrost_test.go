package modelrouter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSharedBifrostRoutesSupportedTasksByLogicalAlias(t *testing.T) {
	cases := []struct {
		task, difficulty, want string
	}{
		{TaskTypeCode, DifficultyEasy, "bifrost/coding"},
		{TaskTypeReview, DifficultyMedium, "bifrost/code-review"},
		{TaskTypeArchitecture, DifficultyHard, "bifrost/agent"},
		{TaskTypeDocs, DifficultyEasy, "bifrost/summarize"},
		{TaskTypeSimple, DifficultyEasy, "bifrost/cheap"},
		{TaskTypeTest, DifficultyExpert, "bifrost/agent"},
	}
	for _, tc := range cases {
		t.Run(tc.task+"-"+tc.difficulty, func(t *testing.T) {
			bifrost := &mockProvider{name: "bifrost", available: true}
			direct := &mockProvider{name: "openai", available: true, models: []ModelInfo{codingModel("direct", "openai", 10)}}
			r := NewRouter(&Config{SharedBifrostRouting: true, MaxCostPer1K: 1}, direct, bifrost)
			result, err := r.RouteCall(context.Background(), CallRequest{
				TaskType: tc.task, Difficulty: tc.difficulty, Messages: []Message{{Role: "user", Content: "review code"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if bifrost.lastReq.PreferredModel != tc.want {
				t.Fatalf("model=%q want=%q", bifrost.lastReq.PreferredModel, tc.want)
			}
			if direct.lastReq.PreferredModel != "" {
				t.Fatal("unapproved direct-provider routing")
			}
			if result.Provider != "bifrost" {
				t.Fatalf("provider=%q", result.Provider)
			}
		})
	}
}

func TestSharedBifrostFailsClosedIfMissingOrFails(t *testing.T) {
	offline := &mockProvider{name: "bifrost", available: false}
	direct := &mockProvider{name: "openai", available: true, models: []ModelInfo{codingModel("direct", "openai", 10)}}
	r := NewRouter(&Config{SharedBifrostRouting: true}, direct, offline)
	_, err := r.RouteCall(context.Background(), CallRequest{TaskType: TaskTypeCode})
	if err == nil {
		t.Fatal("missing gateway must fail closed")
	}
	offline.available = true
	offline.callErr = errors.New("upstream failed")
	_, err = r.RouteCall(context.Background(), CallRequest{TaskType: TaskTypeReview})
	if err == nil {
		t.Fatal("upstream failure must not fall back to ungoverned direct provider")
	}
	if direct.lastReq.PreferredModel != "" {
		t.Fatal("direct bypass must not be used")
	}
}

func TestSharedBifrostDoesNotOverrideExplicitConstraints(t *testing.T) {
	bifrost := &mockProvider{name: "bifrost", available: true}
	direct := &mockProvider{name: "openai", available: true, models: []ModelInfo{codingModel("direct", "openai", 10)}}
	r := NewRouter(&Config{SharedBifrostRouting: true, MaxCostPer1K: 1}, direct, bifrost)
	for _, req := range []CallRequest{
		{TaskType: TaskTypeReview, CostCap: 0.01},
		{TaskType: TaskTypeReview, StructuredReq: true},
		{TaskType: TaskTypeReview, PreferredModel: "explicit-model"},
		{TaskType: TaskTypeReview, ContextSize: 500000},
	} {
		_, err := r.RouteCall(context.Background(), req)
		if err == nil {
			t.Fatalf("constrained request must fail closed: %#v", req)
		}
		if direct.lastReq.PreferredModel != "" {
			t.Fatal("direct fallback would bypass shared governance")
		}
		if bifrost.lastReq.PreferredModel != "" {
			t.Fatalf("shared route ignored explicit constraints: %#v", req)
		}
	}
}

func TestBifrostGatewayURLNormalizesV1ExactlyOnce(t *testing.T) {
	t.Setenv("BIFROST_URL", "https://example.com/")
	provider := NewBifrostProvider()
	if provider.baseURL != "https://example.com/v1" {
		t.Fatalf("baseURL=%q", provider.baseURL)
	}
	t.Setenv("BIFROST_URL", "https://example.com/v1/")
	provider = NewBifrostProvider()
	if provider.baseURL != "https://example.com/v1" {
		t.Fatalf("baseURL=%q", provider.baseURL)
	}
}

// A local fake gateway proves the full HTTP boundary and ensures we report
// the real model after fallback, not only the logical alias that was requested.
func TestSharedBifrostCallsActualV1EndpointAndReturnsActualModel(t *testing.T) {
	t.Setenv("BIFROST_API_KEY", "test-virtual-key")
	var requested string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path=%q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-virtual-key" {
			t.Error("wrong virtual key header")
		}
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		requested = payload.Model
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "openrouter/openai/gpt-6-luna",
			"choices": []any{map[string]any{"message": map[string]any{"content": "success"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer upstream.Close()
	t.Setenv("BIFROST_URL", upstream.URL)
	provider := NewBifrostProvider()
	provider.client = upstream.Client()
	r := NewRouter(&Config{SharedBifrostRouting: true}, provider)
	result, err := r.RouteCall(context.Background(), CallRequest{TaskType: TaskTypeReview, Messages: []Message{{Role: "user", Content: "review"}}})
	if err != nil {
		t.Fatal(err)
	}
	if requested != "bifrost/code-review" {
		t.Fatalf("requested model=%q", requested)
	}
	if result.Model != "openrouter/openai/gpt-6-luna" {
		t.Fatalf("actual model=%q", result.Model)
	}
}

// The shared Bifrost service uses a dedicated virtual-key header for
// governance. Bearer is reserved for separately configured dashboard auth.
func TestSharedBifrostUsesVirtualKeyHeader(t *testing.T) {
	t.Setenv("BIFROST_SHARED_ROUTING_ENABLED", "true")
	t.Setenv("BIFROST_API_KEY", "test-product-virtual-key")
	var called bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := r.Header.Get("X-Bf-Vk"); got != "test-product-virtual-key" {
			t.Errorf("virtual key header not sent")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("shared Bifrost must not mislabel the virtual key as a Bearer token")
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "openrouter/openai/gpt-6-luna",
			"choices": []any{map[string]any{"message": map[string]any{"content": "OK"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 8, "completion_tokens": 2},
		})
	}))
	defer upstream.Close()
	t.Setenv("BIFROST_URL", upstream.URL)
	provider := NewBifrostProvider()
	provider.client = upstream.Client()
	r := NewRouter(DefaultConfig(), provider)
	_, err := r.RouteCall(context.Background(), CallRequest{TaskType: TaskTypeCode, Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("never reached shared gateway")
	}
}

func TestSharedBifrostNeverRedirectsVirtualKey(t *testing.T) {
	t.Setenv("BIFROST_SHARED_ROUTING_ENABLED", "true")
	t.Setenv("BIFROST_API_KEY", "test-private-gateway-key")
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if r.Header.Get("x-bf-vk") != "" {
			t.Error("private Bifrost key leaked through redirect")
		}
	}))
	defer target.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/collect", http.StatusTemporaryRedirect)
	}))
	defer gateway.Close()
	t.Setenv("BIFROST_URL", gateway.URL)
	provider := NewBifrostProvider()
	_, err := provider.Call(context.Background(), CallRequest{
		PreferredModel: "bifrost/coding",
		Messages:       []Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("redirect should fail closed")
	}
	if reached {
		t.Fatal("gateway redirect was followed")
	}
}
