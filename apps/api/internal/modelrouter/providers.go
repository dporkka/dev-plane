package modelrouter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// ProviderNotConnectedError is returned when a provider is not configured.
var ProviderNotConnectedError = errors.New("provider not connected: API key not configured")

// --------------------
// OpenAI Provider
// --------------------

// OpenAIProvider implements the Provider interface for OpenAI.
type OpenAIProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	models  []ModelInfo
	once    sync.Once
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider() *OpenAIProvider {
	return &OpenAIProvider{
		baseURL: envOrDefault("OPENAI_BASE_URL", "https://api.openai.com/v1"),
	}
}

// Name returns the provider name.
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Models returns the OpenAI model catalog.
func (p *OpenAIProvider) Models() []ModelInfo {
	p.once.Do(func() {
		p.models = []ModelInfo{
			{
				Name:                     "gpt-4o",
				Provider:                 "openai",
				MaxContext:               128000,
				CodingStrength:           9,
				ReasoningStrength:        8,
				LatencyMs:                2500,
				CostPer1KInput:           0.005,
				CostPer1KOutput:          0.015,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "gpt-4o-mini",
				Provider:                 "openai",
				MaxContext:               128000,
				CodingStrength:           7,
				ReasoningStrength:        6,
				LatencyMs:                800,
				CostPer1KInput:           0.00015,
				CostPer1KOutput:          0.0006,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "o1-preview",
				Provider:                 "openai",
				MaxContext:               128000,
				CodingStrength:           8,
				ReasoningStrength:        10,
				LatencyMs:                15000,
				CostPer1KInput:           0.015,
				CostPer1KOutput:          0.060,
				SupportsStructuredOutput: false,
				SupportsVision:           false,
				SupportsFunctionCalling:  false,
			},
			{
				Name:                     "o1-mini",
				Provider:                 "openai",
				MaxContext:               128000,
				CodingStrength:           7,
				ReasoningStrength:        8,
				LatencyMs:                4000,
				CostPer1KInput:           0.003,
				CostPer1KOutput:          0.012,
				SupportsStructuredOutput: false,
				SupportsVision:           false,
				SupportsFunctionCalling:  false,
			},
		}
	})
	return p.models
}

// Call executes a model call via the OpenAI API.
func (p *OpenAIProvider) Call(ctx context.Context, req CallRequest) (*CallResult, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("openai: %w", ProviderNotConnectedError)
	}
	return callOpenAICompatible(ctx, p.client, p.baseURL, p.apiKey, p.Name(), req, p.Models())
}

// IsAvailable returns true if the OpenAI provider is configured.
func (p *OpenAIProvider) IsAvailable() bool {
	if p.apiKey == "" {
		p.apiKey = os.Getenv("OPENAI_API_KEY")
	}
	return p.apiKey != ""
}

// --------------------
// Anthropic Provider
// --------------------

// AnthropicProvider implements the Provider interface for Anthropic.
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	models  []ModelInfo
	once    sync.Once
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider() *AnthropicProvider {
	return &AnthropicProvider{
		baseURL: envOrDefault("ANTHROPIC_BASE_URL", "https://api.anthropic.com/v1"),
	}
}

// Name returns the provider name.
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// Models returns the Anthropic model catalog.
func (p *AnthropicProvider) Models() []ModelInfo {
	p.once.Do(func() {
		p.models = []ModelInfo{
			{
				Name:                     "claude-sonnet-4-20250514",
				Provider:                 "anthropic",
				MaxContext:               200000,
				CodingStrength:           9,
				ReasoningStrength:        9,
				LatencyMs:                3000,
				CostPer1KInput:           0.003,
				CostPer1KOutput:          0.015,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "claude-opus-4-20250514",
				Provider:                 "anthropic",
				MaxContext:               200000,
				CodingStrength:           10,
				ReasoningStrength:        10,
				LatencyMs:                8000,
				CostPer1KInput:           0.015,
				CostPer1KOutput:          0.075,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "claude-haiku-4-20250514",
				Provider:                 "anthropic",
				MaxContext:               200000,
				CodingStrength:           6,
				ReasoningStrength:        5,
				LatencyMs:                600,
				CostPer1KInput:           0.00025,
				CostPer1KOutput:          0.00125,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
		}
	})
	return p.models
}

// Call executes a model call via the Anthropic API.
func (p *AnthropicProvider) Call(ctx context.Context, req CallRequest) (*CallResult, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("anthropic: %w", ProviderNotConnectedError)
	}
	return callAnthropic(ctx, p.client, p.baseURL, p.apiKey, req, p.Models())
}

// IsAvailable returns true if the Anthropic provider is configured.
func (p *AnthropicProvider) IsAvailable() bool {
	if p.apiKey == "" {
		p.apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	return p.apiKey != ""
}

// --------------------
// Gemini Provider
// --------------------

// GeminiProvider implements the Provider interface for Google Gemini.
type GeminiProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	models  []ModelInfo
	once    sync.Once
}

// NewGeminiProvider creates a new Gemini provider.
func NewGeminiProvider() *GeminiProvider {
	return &GeminiProvider{
		baseURL: envOrDefault("GEMINI_BASE_URL", "https://generativelanguage.googleapis.com/v1beta"),
	}
}

// Name returns the provider name.
func (p *GeminiProvider) Name() string {
	return "gemini"
}

// Models returns the Gemini model catalog.
func (p *GeminiProvider) Models() []ModelInfo {
	p.once.Do(func() {
		p.models = []ModelInfo{
			{
				Name:                     "gemini-2.5-pro",
				Provider:                 "gemini",
				MaxContext:               1000000,
				CodingStrength:           9,
				ReasoningStrength:        9,
				LatencyMs:                4000,
				CostPer1KInput:           0.00125,
				CostPer1KOutput:          0.01,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "gemini-2.5-flash",
				Provider:                 "gemini",
				MaxContext:               1000000,
				CodingStrength:           7,
				ReasoningStrength:        7,
				LatencyMs:                1200,
				CostPer1KInput:           0.00015,
				CostPer1KOutput:          0.0006,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
		}
	})
	return p.models
}

// Call executes a model call via the Gemini API.
func (p *GeminiProvider) Call(ctx context.Context, req CallRequest) (*CallResult, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("gemini: %w", ProviderNotConnectedError)
	}
	return callGemini(ctx, p.client, p.baseURL, p.apiKey, req, p.Models())
}

// IsAvailable returns true if the Gemini provider is configured.
func (p *GeminiProvider) IsAvailable() bool {
	if p.apiKey == "" {
		p.apiKey = os.Getenv("GEMINI_API_KEY")
	}
	return p.apiKey != ""
}

// --------------------
// Groq Provider
// --------------------

// GroqProvider implements the Provider interface for Groq (fast inference).
type GroqProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	models  []ModelInfo
	once    sync.Once
}

// NewGroqProvider creates a new Groq provider.
func NewGroqProvider() *GroqProvider {
	return &GroqProvider{
		baseURL: envOrDefault("GROQ_BASE_URL", "https://api.groq.com/openai/v1"),
	}
}

// Name returns the provider name.
func (p *GroqProvider) Name() string {
	return "groq"
}

// Models returns the Groq model catalog.
func (p *GroqProvider) Models() []ModelInfo {
	p.once.Do(func() {
		p.models = []ModelInfo{
			{
				Name:                     "llama-4-scout",
				Provider:                 "groq",
				MaxContext:               128000,
				CodingStrength:           7,
				ReasoningStrength:        7,
				LatencyMs:                300,
				CostPer1KInput:           0.00013,
				CostPer1KOutput:          0.00069,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "llama-4-maverick",
				Provider:                 "groq",
				MaxContext:               128000,
				CodingStrength:           8,
				ReasoningStrength:        8,
				LatencyMs:                500,
				CostPer1KInput:           0.0002,
				CostPer1KOutput:          0.00032,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "mixtral-8x7b",
				Provider:                 "groq",
				MaxContext:               32768,
				CodingStrength:           6,
				ReasoningStrength:        6,
				LatencyMs:                200,
				CostPer1KInput:           0.00024,
				CostPer1KOutput:          0.00024,
				SupportsStructuredOutput: true,
				SupportsVision:           false,
				SupportsFunctionCalling:  true,
			},
		}
	})
	return p.models
}

// Call executes a model call via the Groq API.
func (p *GroqProvider) Call(ctx context.Context, req CallRequest) (*CallResult, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("groq: %w", ProviderNotConnectedError)
	}
	return callOpenAICompatible(ctx, p.client, p.baseURL, p.apiKey, p.Name(), req, p.Models())
}

// IsAvailable returns true if the Groq provider is configured.
func (p *GroqProvider) IsAvailable() bool {
	if p.apiKey == "" {
		p.apiKey = os.Getenv("GROQ_API_KEY")
	}
	return p.apiKey != ""
}

// --------------------
// Fireworks Provider
// --------------------

// FireworksProvider implements the Provider interface for Fireworks AI.
type FireworksProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	models  []ModelInfo
	once    sync.Once
}

// NewFireworksProvider creates a new Fireworks provider.
func NewFireworksProvider() *FireworksProvider {
	return &FireworksProvider{
		baseURL: envOrDefault("FIREWORKS_BASE_URL", "https://api.fireworks.ai/inference/v1"),
	}
}

// Name returns the provider name.
func (p *FireworksProvider) Name() string {
	return "fireworks"
}

// Models returns the Fireworks model catalog.
func (p *FireworksProvider) Models() []ModelInfo {
	p.once.Do(func() {
		p.models = []ModelInfo{
			{
				Name:                     "accounts/fireworks/models/deepseek-v3",
				Provider:                 "fireworks",
				MaxContext:               128000,
				CodingStrength:           8,
				ReasoningStrength:        8,
				LatencyMs:                2000,
				CostPer1KInput:           0.0009,
				CostPer1KOutput:          0.0009,
				SupportsStructuredOutput: true,
				SupportsVision:           false,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "accounts/fireworks/models/llama-v3p3-70b-instruct",
				Provider:                 "fireworks",
				MaxContext:               128000,
				CodingStrength:           7,
				ReasoningStrength:        7,
				LatencyMs:                800,
				CostPer1KInput:           0.00013,
				CostPer1KOutput:          0.0005,
				SupportsStructuredOutput: true,
				SupportsVision:           false,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "accounts/fireworks/models/qwen2p5-72b-instruct",
				Provider:                 "fireworks",
				MaxContext:               32768,
				CodingStrength:           7,
				ReasoningStrength:        7,
				LatencyMs:                600,
				CostPer1KInput:           0.0009,
				CostPer1KOutput:          0.0009,
				SupportsStructuredOutput: true,
				SupportsVision:           false,
				SupportsFunctionCalling:  true,
			},
		}
	})
	return p.models
}

// Call executes a model call via the Fireworks API.
func (p *FireworksProvider) Call(ctx context.Context, req CallRequest) (*CallResult, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("fireworks: %w", ProviderNotConnectedError)
	}
	return callOpenAICompatible(ctx, p.client, p.baseURL, p.apiKey, p.Name(), req, p.Models())
}

// IsAvailable returns true if the Fireworks provider is configured.
func (p *FireworksProvider) IsAvailable() bool {
	if p.apiKey == "" {
		p.apiKey = os.Getenv("FIREWORKS_API_KEY")
	}
	return p.apiKey != ""
}

// --------------------
// Bifrost Provider
// --------------------

// BifrostProvider implements the Provider interface for the Bifrost AI gateway.
// Bifrost exposes an OpenAI-compatible chat-completions endpoint.
type BifrostProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	models  []ModelInfo
	once    sync.Once
}

// NewBifrostProvider creates a new Bifrost provider.
func NewBifrostProvider() *BifrostProvider {
	return &BifrostProvider{
		baseURL: envOrDefault("BIFROST_URL", "http://localhost:8081"),
	}
}

// Name returns the provider name.
func (p *BifrostProvider) Name() string { return "bifrost" }

// Models returns the Bifrost model catalog.
func (p *BifrostProvider) Models() []ModelInfo {
	p.once.Do(func() {
		p.models = []ModelInfo{
			{
				Name:                     "bifrost/gpt-4o",
				Provider:                 "bifrost",
				MaxContext:               128000,
				CodingStrength:           9,
				ReasoningStrength:        8,
				LatencyMs:                2500,
				CostPer1KInput:           0.005,
				CostPer1KOutput:          0.015,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "bifrost/claude-sonnet",
				Provider:                 "bifrost",
				MaxContext:               200000,
				CodingStrength:           9,
				ReasoningStrength:        9,
				LatencyMs:                3000,
				CostPer1KInput:           0.003,
				CostPer1KOutput:          0.015,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
			{
				Name:                     "bifrost/gemini-flash",
				Provider:                 "bifrost",
				MaxContext:               1000000,
				CodingStrength:           7,
				ReasoningStrength:        7,
				LatencyMs:                1200,
				CostPer1KInput:           0.00015,
				CostPer1KOutput:          0.0006,
				SupportsStructuredOutput: true,
				SupportsVision:           true,
				SupportsFunctionCalling:  true,
			},
		}
	})
	return p.models
}

// Call executes a model call via the Bifrost gateway.
func (p *BifrostProvider) Call(ctx context.Context, req CallRequest) (*CallResult, error) {
	if !p.IsAvailable() {
		return nil, fmt.Errorf("bifrost: %w", ProviderNotConnectedError)
	}
	return callOpenAICompatible(ctx, p.client, p.baseURL, p.apiKey, p.Name(), req, p.Models())
}

// IsAvailable returns true if the Bifrost provider is configured.
func (p *BifrostProvider) IsAvailable() bool {
	if p.apiKey == "" {
		p.apiKey = os.Getenv("BIFROST_API_KEY")
	}
	return p.apiKey != ""
}

// AllProviders returns a list of all available provider instances.
func AllProviders() []Provider {
	return []Provider{
		NewOpenAIProvider(),
		NewAnthropicProvider(),
		NewGeminiProvider(),
		NewGroqProvider(),
		NewFireworksProvider(),
		NewBifrostProvider(),
	}
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return strings.TrimRight(value, "/")
	}
	return fallback
}
