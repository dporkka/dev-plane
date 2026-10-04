package modelrouter

import (
	"fmt"
	"strings"
)

// Semantic routes are stable workload intents. Concrete models and providers
// are resolved by the model gateway so Dev Plane does not hard-code volatile
// model IDs, prices, or provider health data.
const (
	RouteAuto               = "auto"
	RouteUltraLowLatency    = "ultra_low_latency"
	RouteCheapClassification = "cheap_classification"
	RouteCheapSummary       = "cheap_summary"
	RouteBalancedChat       = "balanced_chat"
	RouteCodingFast         = "coding_fast"
	RouteCodingDeep         = "coding_deep"
	RouteAgenticLongHorizon = "agentic_long_horizon"
	RouteReasoningHigh      = "reasoning_high"
	RouteSecurityReview     = "security_review"
)

var semanticRoutes = map[string]struct{}{
	RouteAuto:                {},
	RouteUltraLowLatency:     {},
	RouteCheapClassification: {},
	RouteCheapSummary:        {},
	RouteBalancedChat:        {},
	RouteCodingFast:          {},
	RouteCodingDeep:          {},
	RouteAgenticLongHorizon:  {},
	RouteReasoningHigh:       {},
	RouteSecurityReview:      {},
}

func normalizeSemanticRoute(route string) (string, error) {
	route = strings.TrimSpace(route)
	if route == "" {
		return RouteAuto, nil
	}
	if _, ok := semanticRoutes[route]; !ok {
		return "", fmt.Errorf("unknown semantic route %q", route)
	}
	return route, nil
}

func routeModelAlias(route string) string {
	return "route/" + route
}

func (r *Router) availableProvider(name string) Provider {
	for _, provider := range r.providers {
		if strings.EqualFold(provider.Name(), name) && provider.IsAvailable() {
			return provider
		}
	}
	return nil
}

// bifrostRoutingHeaders exposes only a fixed allowlist of routing context to
// the gateway. Arbitrary metadata must not silently become upstream headers.
func bifrostRoutingHeaders(req CallRequest) map[string]string {
	headers := map[string]string{}
	set := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			headers[key] = value
		}
	}

	set("x-devplane-route", req.Route)
	set("x-devplane-task-type", req.TaskType)
	set("x-devplane-difficulty", req.Difficulty)
	set("x-devplane-latency", req.LatencyReq)

	if req.RoutingMetadata != nil {
		set("x-devplane-agent-role", req.RoutingMetadata["agent-role"])
		set("x-devplane-risk", req.RoutingMetadata["risk"])
	}
	return headers
}
