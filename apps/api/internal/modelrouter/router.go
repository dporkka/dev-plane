package modelrouter

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// TaskType constants for routing.
const (
	TaskTypeCode         = "code"         // general coding tasks
	TaskTypeRefactor     = "refactor"     // code refactoring
	TaskTypeDebug        = "debug"        // debugging
	TaskTypeReview       = "review"       // code review
	TaskTypeTest         = "test"         // test generation
	TaskTypeDocs         = "docs"         // documentation
	TaskTypeArchitecture = "architecture" // system design
	TaskTypeSimple       = "simple"       // simple/completion tasks
)

// LatencyRequirement constants.
const (
	LatencyFast   = "fast"    // < 2s
	LatencyNormal = "normal"  // < 10s
	LatencySlowOK = "slow_ok" // any latency acceptable
)

// Difficulty constants.
const (
	DifficultyEasy   = "easy"
	DifficultyMedium = "medium"
	DifficultyHard   = "hard"
	DifficultyExpert = "expert"
)

// Message represents a chat message for model calls.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Router selects the best model/provider for a given task.
type Router struct {
	providers []Provider
	config    *Config
}

// Config holds router configuration.
type Config struct {
	// DefaultModel is the fallback when no specific match is found.
	DefaultModel string
	// DefaultProvider is the fallback provider.
	DefaultProvider string
	// MaxCostPer1K is the maximum cost per 1K tokens for any call.
	MaxCostPer1K float64
	// ProviderPriority defines the order of preference when scores tie.
	ProviderPriority []string
}

// DefaultConfig returns sensible default router configuration.
func DefaultConfig() *Config {
	return &Config{
		DefaultModel:     "gpt-4o",
		DefaultProvider:  "openai",
		MaxCostPer1K:     0.10,
		ProviderPriority: []string{"openai", "bifrost", "anthropic", "groq", "fireworks", "gemini"},
	}
}

// Provider is a model provider (OpenAI, Anthropic, etc.)
type Provider interface {
	Name() string
	Models() []ModelInfo
	Call(ctx context.Context, req CallRequest) (*CallResult, error)
	IsAvailable() bool
}

// ModelInfo describes a model's capabilities.
type ModelInfo struct {
	Name                     string
	Provider                 string
	MaxContext               int
	CodingStrength           int // 1-10
	ReasoningStrength        int // 1-10
	LatencyMs                int // typical
	CostPer1KInput           float64
	CostPer1KOutput          float64
	SupportsStructuredOutput bool
	SupportsVision           bool
	SupportsFunctionCalling  bool
}

// CallRequest is a request to call a model.
type CallRequest struct {
	TaskType        string
	Difficulty      string
	LatencyReq      string // fast, normal, slow_ok
	ContextSize     int
	StructuredReq   bool
	CostCap         float64
	PreferredModel  string
	Route           string
	RoutingMetadata map[string]string
	Messages        []Message
}

// CallResult contains the model response + usage and the provenance needed to
// evaluate routing decisions against eventual task outcomes.
type CallResult struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Cost             float64
	LatencyMs        int
	Model            string
	Provider         string
	Route            string
	RouteSource      string
	PolicyVersion    string
	SpendAuthority   string
	FinishReason     string
}

// Score represents the routing score for a model.
type Score struct {
	Model      ModelInfo
	Provider   Provider
	Score      float64
	IsFallback bool
}

// NewRouter creates a new model router.
func NewRouter(config *Config, providers ...Provider) *Router {
	if config == nil {
		config = DefaultConfig()
	}
	return &Router{
		providers: providers,
		config:    config,
	}
}

// RegisterProvider adds a provider to the router.
func (r *Router) RegisterProvider(p Provider) {
	r.providers = append(r.providers, p)
}

// SelectModel chooses the best model for a task based on routing criteria.
func (r *Router) SelectModel(ctx context.Context, taskType, difficulty, latencyReq string, contextSize int, costCap float64) (*ModelInfo, error) {
	var scores []Score

	for _, provider := range r.providers {
		if !provider.IsAvailable() {
			continue
		}
		for _, model := range provider.Models() {
			if contextSize > 0 && model.MaxContext > 0 && contextSize > model.MaxContext {
				continue
			}
			effectiveCostCap := costCap
			if effectiveCostCap == 0 {
				effectiveCostCap = r.config.MaxCostPer1K
			}
			if effectiveCostCap > 0 && model.CostPer1KOutput > effectiveCostCap {
				continue
			}

			s := r.scoreModel(model, taskType, difficulty, latencyReq)
			scores = append(scores, Score{
				Model:    model,
				Provider: provider,
				Score:    s,
			})
		}
	}

	if len(scores) == 0 {
		return r.fallbackModel(), nil
	}

	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Score != scores[j].Score {
			return scores[i].Score > scores[j].Score
		}
		return r.providerRank(scores[i].Model.Provider) < r.providerRank(scores[j].Model.Provider)
	})

	best := &scores[0].Model
	return best, nil
}

// RouteCall selects the best model and executes the call. When Bifrost is
// configured, it is the canonical model gateway: Dev Plane sends a stable
// semantic route and task context, while Bifrost resolves the volatile model,
// provider, health, price, and fallback policy. If Bifrost is unavailable,
// the legacy local selector remains as a compatibility fallback.
func (r *Router) RouteCall(ctx context.Context, req CallRequest) (*CallResult, error) {
	route, err := normalizeSemanticRoute(req.Route)
	if err != nil {
		return nil, err
	}
	source := routeSource(req.Route)

	if bifrost := r.availableProvider("bifrost"); bifrost != nil {
		selectedReq := req
		selectedReq.Route = route
		selectedReq.PreferredModel = routeModelAlias(route)
		result, err := bifrost.Call(ctx, selectedReq)
		if err != nil {
			// Do not silently bypass gateway governance, budgets, or compliance by
			// falling through to direct provider credentials after a gateway error.
			return nil, fmt.Errorf("bifrost route %s: %w", route, err)
		}
		if result.Model == "" {
			result.Model = selectedReq.PreferredModel
		}
		if result.Provider == "" {
			result.Provider = bifrost.Name()
		}
		result.Route = route
		result.RouteSource = source
		result.PolicyVersion = SemanticRoutingPolicyVersion
		result.SpendAuthority = SpendAuthorityGateway
		return result, nil
	}

	modelInfo, err := r.SelectModel(ctx, req.TaskType, req.Difficulty, req.LatencyReq, req.ContextSize, req.CostCap)
	if err != nil {
		return nil, fmt.Errorf("select model: %w", err)
	}

	var selectedProvider Provider
	for _, p := range r.providers {
		if p.Name() == modelInfo.Provider && p.IsAvailable() {
			selectedProvider = p
			break
		}
	}

	if selectedProvider == nil {
		return nil, fmt.Errorf("no available provider for model %s", modelInfo.Name)
	}

	selectedReq := req
	selectedReq.Route = route
	selectedReq.PreferredModel = modelInfo.Name
	result, err := selectedProvider.Call(ctx, selectedReq)
	if err != nil {
		result, err = r.tryFallback(ctx, selectedReq, modelInfo)
		if err != nil {
			return nil, err
		}
	} else {
		result.Model = modelInfo.Name
		result.Provider = modelInfo.Provider
	}

	result.Route = route
	result.RouteSource = RouteSourceLegacyLocal
	result.PolicyVersion = SemanticRoutingPolicyVersion
	result.SpendAuthority = SpendAuthorityDevPlane
	return result, nil
}

// tryFallback attempts to call the next best available model.
func (r *Router) tryFallback(ctx context.Context, req CallRequest, failedModel *ModelInfo) (*CallResult, error) {
	for _, provider := range r.providers {
		if !provider.IsAvailable() {
			continue
		}
		for _, model := range provider.Models() {
			if model.Name == failedModel.Name {
				continue
			}
			fallbackReq := req
			fallbackReq.PreferredModel = model.Name
			result, err := provider.Call(ctx, fallbackReq)
			if err == nil {
				result.Model = model.Name
				result.Provider = model.Provider
				return result, nil
			}
		}
	}
	return nil, fmt.Errorf("all providers failed for task type %s", req.TaskType)
}

// scoreModel calculates a routing score for a model given the task requirements.
func (r *Router) scoreModel(model ModelInfo, taskType, difficulty, latencyReq string) float64 {
	score := 50.0

	switch taskType {
	case TaskTypeCode, TaskTypeRefactor:
		score += float64(model.CodingStrength) * 5
		if model.SupportsStructuredOutput {
			score += 5
		}
	case TaskTypeDebug:
		score += float64(model.ReasoningStrength) * 4
		score += float64(model.CodingStrength) * 3
	case TaskTypeReview:
		score += float64(model.ReasoningStrength) * 5
	case TaskTypeTest:
		score += float64(model.CodingStrength) * 4
		if model.SupportsStructuredOutput {
			score += 5
		}
	case TaskTypeArchitecture:
		score += float64(model.ReasoningStrength) * 6
		score += float64(model.MaxContext) / 4000.0
	case TaskTypeDocs:
		score += float64(model.CodingStrength) * 2
		score -= model.CostPer1KOutput * 100
	case TaskTypeSimple:
		score -= model.CostPer1KOutput * 200
		score -= float64(model.LatencyMs) / 50.0
	}

	switch difficulty {
	case DifficultyEasy:
		score -= model.CostPer1KOutput * 50
	case DifficultyHard, DifficultyExpert:
		score += float64(model.ReasoningStrength) * 3
		score += float64(model.CodingStrength) * 2
	}

	switch latencyReq {
	case LatencyFast:
		score -= float64(model.LatencyMs) / 20.0
	case LatencyNormal:
		score -= float64(model.LatencyMs) / 50.0
	case LatencySlowOK:
	}

	score -= model.CostPer1KOutput * 30
	return score
}

// providerRank returns the priority rank of a provider (lower = higher priority).
func (r *Router) providerRank(name string) int {
	for i, p := range r.config.ProviderPriority {
		if strings.EqualFold(p, name) {
			return i
		}
	}
	return len(r.config.ProviderPriority)
}

// fallbackModel returns the default model info when no match is found.
func (r *Router) fallbackModel() *ModelInfo {
	return &ModelInfo{
		Name:                     r.config.DefaultModel,
		Provider:                 r.config.DefaultProvider,
		MaxContext:               128000,
		CodingStrength:           8,
		ReasoningStrength:        8,
		LatencyMs:                3000,
		CostPer1KInput:           0.005,
		CostPer1KOutput:          0.015,
		SupportsStructuredOutput: true,
		SupportsVision:           true,
		SupportsFunctionCalling:  true,
	}
}

// AllModels returns all models from all registered providers.
func (r *Router) AllModels() []ModelInfo {
	var all []ModelInfo
	for _, p := range r.providers {
		all = append(all, p.Models()...)
	}
	return all
}

// AvailableProviders returns names of all available providers.
func (r *Router) AvailableProviders() []string {
	var names []string
	for _, p := range r.providers {
		if p.IsAvailable() {
			names = append(names, p.Name())
		}
	}
	return names
}
