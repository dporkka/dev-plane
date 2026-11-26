package modelrouter

import (
	"fmt"
	"strings"
)

// SemanticRoutingPolicyVersion changes only when route semantics or precedence
// change. Concrete model mappings live in Bifrost and may change independently.
const SemanticRoutingPolicyVersion = "devplane-semantic-v1"

// Semantic routes are stable workload intents. Concrete models and providers
// are resolved by the model gateway so Dev Plane does not hard-code volatile
// model IDs, prices, or provider health data.
const (
	RouteAuto                = "auto"
	RouteUltraLowLatency     = "ultra_low_latency"
	RouteCheapClassification = "cheap_classification"
	RouteCheapSummary        = "cheap_summary"
	RouteBalancedChat        = "balanced_chat"
	RouteCodingFast          = "coding_fast"
	RouteCodingDeep          = "coding_deep"
	RouteAgenticLongHorizon  = "agentic_long_horizon"
	RouteReasoningHigh       = "reasoning_high"
	RouteSecurityReview      = "security_review"
)

const (
	RouteSourceAuto        = "auto"
	RouteSourceExplicit    = "explicit"
	RouteSourceLegacyLocal = "legacy_local"
)

const (
	SpendAuthorityDevPlane = "dev-plane"
	SpendAuthorityGateway  = "gateway"
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

func routeSource(route string) string {
	route = strings.TrimSpace(route)
	if route == "" || route == RouteAuto {
		return RouteSourceAuto
	}
	return RouteSourceExplicit
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

// UsesExternalSpendAuthority reports whether model spend is enforced by the
// configured model gateway rather than Dev Plane's local price estimates.
func (r *Router) UsesExternalSpendAuthority() bool {
	return r != nil && r.availableProvider("bifrost") != nil
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
