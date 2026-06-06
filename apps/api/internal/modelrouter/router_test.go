package modelrouter

import (
	"context"
	"errors"
	"testing"
)

// mockProvider is a test provider with configurable behavior.
type mockProvider struct {
	name      string
	available bool
	models    []ModelInfo
	callErr   error
	lastReq   CallRequest
}

func (p *mockProvider) Name() string        { return p.name }
func (p *mockProvider) Models() []ModelInfo { return p.models }
func (p *mockProvider) IsAvailable() bool   { return p.available }
func (p *mockProvider) Call(ctx context.Context, req CallRequest) (*CallResult, error) {
	p.lastReq = req
	if p.callErr != nil {
		return nil, p.callErr
	}
	return &CallResult{
		Content:      "test response",
		PromptTokens: 10,
		TotalTokens:  15,
		Cost:         0.001,
		LatencyMs:    100,
	}, nil
}

// codingModel returns a model optimized for coding tasks.
func codingModel(name, provider string, codingStrength int) ModelInfo {
	return ModelInfo{
		Name:                     name,
		Provider:                 provider,
		MaxContext:               128000,
		CodingStrength:           codingStrength,
		ReasoningStrength:        5,
		LatencyMs:                2000,
		CostPer1KInput:           0.005,
		CostPer1KOutput:          0.015,
		SupportsStructuredOutput: true,
		SupportsVision:           true,
		SupportsFunctionCalling:  true,
	}
}

// reasoningModel returns a model optimized for reasoning tasks.
func reasoningModel(name, provider string, reasoningStrength int) ModelInfo {
	return ModelInfo{
		Name:                     name,
		Provider:                 provider,
		MaxContext:               128000,
		CodingStrength:           5,
		ReasoningStrength:        reasoningStrength,
		LatencyMs:                3000,
		CostPer1KInput:           0.005,
		CostPer1KOutput:          0.015,
		SupportsStructuredOutput: true,
		SupportsVision:           true,
		SupportsFunctionCalling:  true,
	}
}

// cheapFastModel returns a low-cost, fast model.
func cheapFastModel(name, provider string) ModelInfo {
	return ModelInfo{
		Name:                     name,
		Provider:                 provider,
		MaxContext:               32000,
		CodingStrength:           5,
		ReasoningStrength:        5,
		LatencyMs:                200,
		CostPer1KInput:           0.0001,
		CostPer1KOutput:          0.0003,
		SupportsStructuredOutput: false,
		SupportsVision:           false,
		SupportsFunctionCalling:  false,
	}
}

// expensiveSlowModel returns a high-cost, slow model.
func expensiveSlowModel(name, provider string) ModelInfo {
	return ModelInfo{
		Name:                     name,
		Provider:                 provider,
		MaxContext:               128000,
		CodingStrength:           8,
		ReasoningStrength:        10,
		LatencyMs:                15000,
		CostPer1KInput:           0.015,
		CostPer1KOutput:          0.060,
		SupportsStructuredOutput: false,
		SupportsVision:           false,
		SupportsFunctionCalling:  false,
	}
}

// TestRouter_SelectModel_CodingTask verifies coding tasks select high coding strength models.
func TestRouter_SelectModel_CodingTask(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			codingModel("gpt-coder", "openai", 9),
			reasoningModel("gpt-reasoner", "openai", 9),
		},
	}
	p2 := &mockProvider{
		name:      "anthropic",
		available: true,
		models: []ModelInfo{
			codingModel("claude-coder", "anthropic", 10),
		},
	}

	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     0.10,
		ProviderPriority: []string{"openai", "anthropic"},
	}

	router := NewRouter(config, p1, p2)
	model, err := router.SelectModel(context.Background(), TaskTypeCode, DifficultyMedium, LatencyNormal, 0, 0)
	if err != nil {
		t.Fatalf("SelectModel() error: %v", err)
	}
	if model == nil {
		t.Fatal("SelectModel() returned nil")
	}
	// claude-coder has coding strength 10, gpt-coder has 9
	if model.Name != "claude-coder" {
		t.Errorf("expected claude-coder for coding task, got %s", model.Name)
	}
}

// TestRouter_SelectModel_ReasoningTask verifies reasoning tasks select high reasoning strength models.
func TestRouter_SelectModel_ReasoningTask(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			codingModel("gpt-coder", "openai", 9),
			reasoningModel("gpt-reasoner", "openai", 8),
		},
	}
	p2 := &mockProvider{
		name:      "anthropic",
		available: true,
		models: []ModelInfo{
			reasoningModel("claude-reasoner", "anthropic", 10),
		},
	}

	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     0.10,
		ProviderPriority: []string{"openai", "anthropic"},
	}

	router := NewRouter(config, p1, p2)
	model, err := router.SelectModel(context.Background(), TaskTypeArchitecture, DifficultyHard, LatencySlowOK, 0, 0)
	if err != nil {
		t.Fatalf("SelectModel() error: %v", err)
	}
	if model == nil {
		t.Fatal("SelectModel() returned nil")
	}
	if model.Name != "claude-reasoner" {
		t.Errorf("expected claude-reasoner for reasoning task, got %s", model.Name)
	}
}

// TestRouter_SelectModel_CostCap filters out expensive models.
func TestRouter_SelectModel_CostCap(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			cheapFastModel("gpt-mini", "openai"),
			expensiveSlowModel("gpt-expensive", "openai"),
		},
	}

	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     0.10,
		ProviderPriority: []string{"openai"},
	}

	router := NewRouter(config, p1)

	// With a low cost cap, expensive model should be filtered out
	model, err := router.SelectModel(context.Background(), TaskTypeCode, DifficultyMedium, LatencyNormal, 0, 0.01)
	if err != nil {
		t.Fatalf("SelectModel() error: %v", err)
	}
	if model == nil {
		t.Fatal("SelectModel() returned nil")
	}
	if model.Name != "gpt-mini" {
		t.Errorf("expected cheap model, got %s (cost: %.4f)", model.Name, model.CostPer1KOutput)
	}
}

// TestRouter_SelectModel_LatencyRequirement filters slow models.
func TestRouter_SelectModel_LatencyRequirement(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			cheapFastModel("gpt-fast", "openai"),
			expensiveSlowModel("gpt-slow", "openai"),
		},
	}

	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1.0,
		ProviderPriority: []string{"openai"},
	}

	router := NewRouter(config, p1)

	// Fast latency requirement should penalize slow models
	model, err := router.SelectModel(context.Background(), TaskTypeSimple, DifficultyEasy, LatencyFast, 0, 0)
	if err != nil {
		t.Fatalf("SelectModel() error: %v", err)
	}
	if model == nil {
		t.Fatal("SelectModel() returned nil")
	}
	// The fast model should score higher with LatencyFast
	if model.LatencyMs > 1000 {
		t.Errorf("expected fast model with LatencyFast, got %s (%d ms)", model.Name, model.LatencyMs)
	}
}

// TestRouter_SelectModel_ContextSize filters small-context models.
func TestRouter_SelectModel_ContextSize(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{
				Name:              "small-context",
				Provider:          "openai",
				MaxContext:        4000,
				CodingStrength:    5,
				ReasoningStrength: 5,
				LatencyMs:         500,
				CostPer1KOutput:   0.01,
			},
			{
				Name:              "large-context",
				Provider:          "openai",
				MaxContext:        128000,
				CodingStrength:    5,
				ReasoningStrength: 5,
				LatencyMs:         500,
				CostPer1KOutput:   0.01,
			},
		},
	}

	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1.0,
		ProviderPriority: []string{"openai"},
	}

	router := NewRouter(config, p1)

	// Require more context than small-context can handle
	model, err := router.SelectModel(context.Background(), TaskTypeCode, DifficultyMedium, LatencyNormal, 50000, 0)
	if err != nil {
		t.Fatalf("SelectModel() error: %v", err)
	}
	if model == nil {
		t.Fatal("SelectModel() returned nil")
	}
	if model.Name != "large-context" {
		t.Errorf("expected large-context model, got %s", model.Name)
	}
}

// TestRouter_SelectModel_NoMatchingModel returns fallback when no models match.
func TestRouter_SelectModel_NoMatchingModel(t *testing.T) {
	// Provider not available
	p1 := &mockProvider{
		name:      "openai",
		available: false,
		models:    []ModelInfo{{Name: "gpt-4o", Provider: "openai", MaxContext: 128000, CostPer1KOutput: 0.01}},
	}

	config := &Config{
		DefaultModel:     "fallback-model",
		DefaultProvider:  "fallback",
		MaxCostPer1K:     1.0,
		ProviderPriority: []string{"openai"},
	}

	router := NewRouter(config, p1)

	// No providers available, should return fallback model
	model, err := router.SelectModel(context.Background(), TaskTypeCode, DifficultyMedium, LatencyNormal, 0, 0)
	if err != nil {
		t.Fatalf("SelectModel() error: %v", err)
	}
	if model == nil {
		t.Fatal("SelectModel() returned nil")
	}
	if model.Name != "fallback-model" {
		t.Errorf("expected fallback model, got %s", model.Name)
	}
}

// TestRouter_SelectModel_NoProvidersAvailable returns fallback.
func TestRouter_SelectModel_NoProvidersAvailable(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: false,
		models: []ModelInfo{
			{Name: "gpt-4o", Provider: "openai", MaxContext: 128000, CostPer1KOutput: 0.01},
		},
	}
	p2 := &mockProvider{
		name:      "anthropic",
		available: false,
		models: []ModelInfo{
			{Name: "claude", Provider: "anthropic", MaxContext: 200000, CostPer1KOutput: 0.01},
		},
	}

	config := DefaultConfig()
	router := NewRouter(config, p1, p2)

	model, err := router.SelectModel(context.Background(), TaskTypeCode, DifficultyMedium, LatencyNormal, 0, 0)
	if err != nil {
		t.Fatalf("SelectModel() error: %v", err)
	}
	if model == nil {
		t.Fatal("SelectModel() returned nil")
	}
	// Should return fallback model
	if model.Name != config.DefaultModel {
		t.Errorf("expected default model %s, got %s", config.DefaultModel, model.Name)
	}
}

// TestRouter_RouteCall verifies call routing to the correct provider.
func TestRouter_RouteCall(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{Name: "gpt-4o", Provider: "openai", MaxContext: 128000, CodingStrength: 9, ReasoningStrength: 8, LatencyMs: 2500, CostPer1KOutput: 0.015, SupportsStructuredOutput: true, SupportsVision: true, SupportsFunctionCalling: true},
		},
	}

	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1.0,
		ProviderPriority: []string{"openai"},
	}

	router := NewRouter(config, p1)
	result, err := router.RouteCall(context.Background(), CallRequest{
		TaskType:   TaskTypeCode,
		Difficulty: DifficultyMedium,
		LatencyReq: LatencyNormal,
		Messages:   []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("RouteCall() error: %v", err)
	}
	if result == nil {
		t.Fatal("RouteCall() returned nil")
	}
	if result.Model != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %s", result.Model)
	}
	if result.Provider != "openai" {
		t.Errorf("expected provider openai, got %s", result.Provider)
	}
	if p1.lastReq.PreferredModel != "gpt-4o" {
		t.Errorf("expected selected model to be passed to provider, got %q", p1.lastReq.PreferredModel)
	}
}

// TestRouter_RouteCall_FallbackChain tests fallback when primary provider fails.
func TestRouter_RouteCall_FallbackChain(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{Name: "gpt-4o", Provider: "openai", MaxContext: 128000, CodingStrength: 9, ReasoningStrength: 8, LatencyMs: 2500, CostPer1KOutput: 0.015},
		},
		callErr: errors.New("openai api error"),
	}
	p2 := &mockProvider{
		name:      "anthropic",
		available: true,
		models: []ModelInfo{
			{Name: "claude-sonnet", Provider: "anthropic", MaxContext: 200000, CodingStrength: 9, ReasoningStrength: 9, LatencyMs: 3000, CostPer1KOutput: 0.015},
		},
	}

	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1.0,
		ProviderPriority: []string{"openai", "anthropic"},
	}

	router := NewRouter(config, p1, p2)
	result, err := router.RouteCall(context.Background(), CallRequest{
		TaskType:   TaskTypeCode,
		Difficulty: DifficultyMedium,
		LatencyReq: LatencyNormal,
		Messages:   []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("RouteCall() error: %v", err)
	}
	if result == nil {
		t.Fatal("RouteCall() returned nil")
	}
	// Should have fallen back to anthropic provider
	if result.Provider != "anthropic" {
		t.Errorf("expected fallback to anthropic, got provider %s", result.Provider)
	}
	if result.Model != "claude-sonnet" {
		t.Errorf("expected claude-sonnet model, got %s", result.Model)
	}
}

// TestRouter_RouteCall_NoProviderForModel returns error.
func TestRouter_RouteCall_NoProviderForModel(t *testing.T) {
	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1.0,
		ProviderPriority: []string{"openai"},
	}

	// No providers at all
	router := NewRouter(config)
	_, err := router.RouteCall(context.Background(), CallRequest{
		TaskType:   TaskTypeCode,
		Difficulty: DifficultyMedium,
		Messages:   []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Error("expected error when no providers available")
	}
}

// TestRouter_AvailableProviders lists only available providers.
func TestRouter_AvailableProviders(t *testing.T) {
	p1 := &mockProvider{name: "openai", available: true, models: []ModelInfo{}}
	p2 := &mockProvider{name: "anthropic", available: false, models: []ModelInfo{}}
	p3 := &mockProvider{name: "gemini", available: true, models: []ModelInfo{}}

	router := NewRouter(DefaultConfig(), p1, p2, p3)
	available := router.AvailableProviders()

	if len(available) != 2 {
		t.Errorf("expected 2 available providers, got %d: %v", len(available), available)
	}

	expected := map[string]bool{"openai": false, "gemini": false}
	for _, name := range available {
		expected[name] = true
	}
	for name, found := range expected {
		if !found {
			t.Errorf("expected %s in available providers", name)
		}
	}
	if containsStr(available, "anthropic") {
		t.Error("did not expect anthropic in available providers")
	}
}

// TestRouter_AllModels returns all models from all providers.
func TestRouter_AllModels(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{Name: "gpt-4o", Provider: "openai"},
			{Name: "gpt-4o-mini", Provider: "openai"},
		},
	}
	p2 := &mockProvider{
		name:      "anthropic",
		available: true,
		models: []ModelInfo{
			{Name: "claude-sonnet", Provider: "anthropic"},
		},
	}

	router := NewRouter(DefaultConfig(), p1, p2)
	all := router.AllModels()
	if len(all) != 3 {
		t.Errorf("expected 3 models, got %d", len(all))
	}
}

// TestRouter_RegisterProvider adds a provider dynamically.
func TestRouter_RegisterProvider(t *testing.T) {
	router := NewRouter(DefaultConfig())
	if len(router.AvailableProviders()) != 0 {
		t.Error("expected no providers initially")
	}

	p := &mockProvider{name: "openai", available: true, models: []ModelInfo{{Name: "gpt-4o", Provider: "openai", MaxContext: 128000, CostPer1KOutput: 0.01}}}
	router.RegisterProvider(p)

	available := router.AvailableProviders()
	if len(available) != 1 || available[0] != "openai" {
		t.Errorf("expected [openai], got %v", available)
	}
}

// TestDefaultConfig verifies default router configuration.
func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	if config.DefaultModel != "gpt-4o" {
		t.Errorf("expected default model gpt-4o, got %s", config.DefaultModel)
	}
	if config.DefaultProvider != "openai" {
		t.Errorf("expected default provider openai, got %s", config.DefaultProvider)
	}
	if config.MaxCostPer1K != 0.10 {
		t.Errorf("expected max cost 0.10, got %.4f", config.MaxCostPer1K)
	}
	if len(config.ProviderPriority) != 5 {
		t.Errorf("expected 5 providers in priority, got %d", len(config.ProviderPriority))
	}
}

// TestProvider_Models verifies each real provider returns expected models.
func TestProvider_Models(t *testing.T) {
	tests := []struct {
		name       string
		provider   Provider
		minModels  int
		modelNames []string
	}{
		{
			name:       "OpenAI",
			provider:   NewOpenAIProvider(),
			minModels:  3,
			modelNames: []string{"gpt-4o", "gpt-4o-mini", "o1-preview", "o1-mini"},
		},
		{
			name:       "Anthropic",
			provider:   NewAnthropicProvider(),
			minModels:  2,
			modelNames: []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514", "claude-haiku-4-20250514"},
		},
		{
			name:       "Gemini",
			provider:   NewGeminiProvider(),
			minModels:  1,
			modelNames: []string{"gemini-2.5-pro", "gemini-2.5-flash"},
		},
		{
			name:       "Groq",
			provider:   NewGroqProvider(),
			minModels:  2,
			modelNames: []string{"llama-4-scout", "llama-4-maverick", "mixtral-8x7b"},
		},
		{
			name:       "Fireworks",
			provider:   NewFireworksProvider(),
			minModels:  2,
			modelNames: []string{"accounts/fireworks/models/deepseek-v3", "accounts/fireworks/models/llama-v3p3-70b-instruct", "accounts/fireworks/models/qwen2p5-72b-instruct"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := tt.provider.Models()
			if len(models) < tt.minModels {
				t.Errorf("expected at least %d models, got %d", tt.minModels, len(models))
			}

			nameSet := make(map[string]bool)
			for _, m := range models {
				nameSet[m.Name] = true
			}
			for _, expected := range tt.modelNames {
				if !nameSet[expected] {
					t.Errorf("expected model %s not found", expected)
				}
			}
		})
	}
}

// TestProvider_IsAvailable verifies availability check based on env var.
func TestProvider_IsAvailable(t *testing.T) {
	t.Run("OpenAI without env var", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		p := NewOpenAIProvider()
		if p.IsAvailable() {
			t.Error("expected OpenAI provider to be unavailable without API key")
		}
	})

	t.Run("OpenAI with env var", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "test-key-openai")
		p := NewOpenAIProvider()
		if !p.IsAvailable() {
			t.Error("expected OpenAI provider to be available with API key")
		}
	})

	t.Run("Anthropic without env var", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		p := NewAnthropicProvider()
		if p.IsAvailable() {
			t.Error("expected Anthropic provider to be unavailable without API key")
		}
	})

	t.Run("Anthropic with env var", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "test-key-anthropic")
		p := NewAnthropicProvider()
		if !p.IsAvailable() {
			t.Error("expected Anthropic provider to be available with API key")
		}
	})

	t.Run("Gemini without env var", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		p := NewGeminiProvider()
		if p.IsAvailable() {
			t.Error("expected Gemini provider to be unavailable without API key")
		}
	})

	t.Run("Gemini with env var", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "test-key-gemini")
		p := NewGeminiProvider()
		if !p.IsAvailable() {
			t.Error("expected Gemini provider to be available with API key")
		}
	})

	t.Run("Groq without env var", func(t *testing.T) {
		t.Setenv("GROQ_API_KEY", "")
		p := NewGroqProvider()
		if p.IsAvailable() {
			t.Error("expected Groq provider to be unavailable without API key")
		}
	})

	t.Run("Groq with env var", func(t *testing.T) {
		t.Setenv("GROQ_API_KEY", "test-key-groq")
		p := NewGroqProvider()
		if !p.IsAvailable() {
			t.Error("expected Groq provider to be available with API key")
		}
	})

	t.Run("Fireworks without env var", func(t *testing.T) {
		t.Setenv("FIREWORKS_API_KEY", "")
		p := NewFireworksProvider()
		if p.IsAvailable() {
			t.Error("expected Fireworks provider to be unavailable without API key")
		}
	})

	t.Run("Fireworks with env var", func(t *testing.T) {
		t.Setenv("FIREWORKS_API_KEY", "test-key-fireworks")
		p := NewFireworksProvider()
		if !p.IsAvailable() {
			t.Error("expected Fireworks provider to be available with API key")
		}
	})
}

// TestProvider_Name verifies provider names.
func TestProvider_Name(t *testing.T) {
	tests := []struct {
		provider Provider
		want     string
	}{
		{NewOpenAIProvider(), "openai"},
		{NewAnthropicProvider(), "anthropic"},
		{NewGeminiProvider(), "gemini"},
		{NewGroqProvider(), "groq"},
		{NewFireworksProvider(), "fireworks"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.provider.Name(); got != tt.want {
				t.Errorf("Name() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestProvider_Call_NotAvailable returns error when not configured.
func TestProvider_Call_NotAvailable(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	p := NewOpenAIProvider()
	_, err := p.Call(context.Background(), CallRequest{})
	if err == nil {
		t.Error("expected error when provider not available")
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	p2 := NewAnthropicProvider()
	_, err = p2.Call(context.Background(), CallRequest{})
	if err == nil {
		t.Error("expected error when anthropic provider not available")
	}
}

// TestAllProviders returns the expected list of providers.
func TestAllProviders(t *testing.T) {
	providers := AllProviders()
	if len(providers) != 5 {
		t.Errorf("expected 5 providers, got %d", len(providers))
	}

	expected := map[string]bool{
		"openai": false, "anthropic": false, "gemini": false,
		"groq": false, "fireworks": false,
	}
	for _, p := range providers {
		expected[p.Name()] = true
	}
	for name, found := range expected {
		if !found {
			t.Errorf("expected provider %s not found", name)
		}
	}
}

// TestNewRouter_NilConfig uses default config.
func TestNewRouter_NilConfig(t *testing.T) {
	router := NewRouter(nil)
	if router.config == nil {
		t.Fatal("expected default config when nil passed")
	}
	if router.config.DefaultModel != "gpt-4o" {
		t.Errorf("expected default model, got %s", router.config.DefaultModel)
	}
}

// TestRouter_scoreModel verifies scoring produces different results for different tasks.
func TestRouter_scoreModel(t *testing.T) {
	config := DefaultConfig()
	router := NewRouter(config)

	codingModel := ModelInfo{CodingStrength: 9, ReasoningStrength: 5, MaxContext: 128000, CostPer1KOutput: 0.015, LatencyMs: 2500, SupportsStructuredOutput: true}
	reasoningModel := ModelInfo{CodingStrength: 5, ReasoningStrength: 10, MaxContext: 200000, CostPer1KOutput: 0.060, LatencyMs: 8000, SupportsStructuredOutput: false}

	codingScore := router.scoreModel(codingModel, TaskTypeCode, DifficultyMedium, LatencyNormal)
	reasoningScore := router.scoreModel(reasoningModel, TaskTypeCode, DifficultyMedium, LatencyNormal)

	if codingScore <= reasoningScore {
		t.Errorf("coding model should score higher for code tasks: coding=%.2f, reasoning=%.2f", codingScore, reasoningScore)
	}

	// Reverse for architecture tasks
	archCodingScore := router.scoreModel(codingModel, TaskTypeArchitecture, DifficultyHard, LatencySlowOK)
	archReasoningScore := router.scoreModel(reasoningModel, TaskTypeArchitecture, DifficultyHard, LatencySlowOK)

	if archReasoningScore <= archCodingScore {
		t.Errorf("reasoning model should score higher for architecture tasks: coding=%.2f, reasoning=%.2f", archCodingScore, archReasoningScore)
	}
}

// TestRouter_providerRank verifies provider priority ranking.
func TestRouter_providerRank(t *testing.T) {
	config := &Config{ProviderPriority: []string{"openai", "anthropic", "groq"}}
	router := NewRouter(config)

	if router.providerRank("openai") != 0 {
		t.Errorf("expected openai rank 0, got %d", router.providerRank("openai"))
	}
	if router.providerRank("anthropic") != 1 {
		t.Errorf("expected anthropic rank 1, got %d", router.providerRank("anthropic"))
	}
	if router.providerRank("unknown") != 3 {
		t.Errorf("expected unknown rank 3, got %d", router.providerRank("unknown"))
	}
}

// TestRouter_fallbackModel verifies fallback model configuration.
func TestRouter_fallbackModel(t *testing.T) {
	config := DefaultConfig()
	router := NewRouter(config)
	fallback := router.fallbackModel()

	if fallback.Name != config.DefaultModel {
		t.Errorf("expected fallback name %s, got %s", config.DefaultModel, fallback.Name)
	}
	if fallback.Provider != config.DefaultProvider {
		t.Errorf("expected fallback provider %s, got %s", config.DefaultProvider, fallback.Provider)
	}
	if fallback.MaxContext != 128000 {
		t.Errorf("expected fallback max context 128000, got %d", fallback.MaxContext)
	}
}

// TestRouter_SelectModel_DocsTaskPrefersCheaper verifies docs tasks prefer cheaper models.
func TestRouter_SelectModel_DocsTaskPrefersCheaper(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{
				Name:              "cheap-docs",
				Provider:          "openai",
				MaxContext:        128000,
				CodingStrength:    5,
				ReasoningStrength: 5,
				LatencyMs:         1000,
				CostPer1KOutput:   0.001,
			},
			{
				Name:              "expensive-docs",
				Provider:          "openai",
				MaxContext:        128000,
				CodingStrength:    5,
				ReasoningStrength: 5,
				LatencyMs:         1000,
				CostPer1KOutput:   0.100,
			},
		},
	}

	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1.0,
		ProviderPriority: []string{"openai"},
	}

	router := NewRouter(config, p1)
	model, err := router.SelectModel(context.Background(), TaskTypeDocs, DifficultyEasy, LatencyNormal, 0, 0)
	if err != nil {
		t.Fatalf("SelectModel() error: %v", err)
	}
	if model.Name != "cheap-docs" {
		t.Errorf("expected cheap model for docs task, got %s (cost: %.4f)", model.Name, model.CostPer1KOutput)
	}
}

// TestRouter_SelectModel_SimpleTaskPrefersFastCheap verifies simple tasks prefer fast, cheap models.
func TestRouter_SelectModel_SimpleTaskPrefersFastCheap(t *testing.T) {
	p1 := &mockProvider{
		name:      "openai",
		available: true,
		models: []ModelInfo{
			{
				Name:              "fast-cheap",
				Provider:          "openai",
				MaxContext:        32000,
				CodingStrength:    5,
				ReasoningStrength: 5,
				LatencyMs:         200,
				CostPer1KOutput:   0.0005,
			},
			{
				Name:              "slow-expensive",
				Provider:          "openai",
				MaxContext:        128000,
				CodingStrength:    10,
				ReasoningStrength: 10,
				LatencyMs:         10000,
				CostPer1KOutput:   0.050,
			},
		},
	}

	config := &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     1.0,
		ProviderPriority: []string{"openai"},
	}

	router := NewRouter(config, p1)
	model, err := router.SelectModel(context.Background(), TaskTypeSimple, DifficultyEasy, LatencyFast, 0, 0)
	if err != nil {
		t.Fatalf("SelectModel() error: %v", err)
	}
	if model.Name != "fast-cheap" {
		t.Errorf("expected fast/cheap model for simple task, got %s (latency: %d ms, cost: %.4f)",
			model.Name, model.LatencyMs, model.CostPer1KOutput)
	}
}

// containsStr checks if a slice contains a string.
func containsStr(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
