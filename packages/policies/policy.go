package policies

import (
	"context"
	"fmt"

	"github.com/ai-dev-control-plane/models"
)

// Effect is the result of a policy evaluation.
type Effect string

const (
	EffectAllow     Effect = "allow"
	EffectAsk       Effect = "ask"
	EffectDeny      Effect = "deny"
	EffectAdminOnly Effect = "admin_only"
)

// MoreRestrictiveThan returns true if e is more restrictive than other.
// Order: allow < ask < deny < admin_only
func (e Effect) MoreRestrictiveThan(other Effect) bool {
	order := map[Effect]int{
		EffectAllow: 0, EffectAsk: 1, EffectDeny: 2, EffectAdminOnly: 3,
	}
	return order[e] > order[other]
}

// String returns the string representation of the effect.
func (e Effect) String() string {
	return string(e)
}

// Policy defines a rule that governs access to resources.
type Policy struct {
	ID           string         `json:"id"`
	OrgID        string         `json:"organization_id"`
	ProjectID    *string        `json:"project_id,omitempty"`
	Name         string         `json:"name"`
	ResourceType string         `json:"resource_type"` // file, command, secret, deploy, git, network
	Action       string         `json:"action"`        // read, write, execute, delete
	Effect       Effect         `json:"effect"`        // allow, ask, deny, admin_only
	Conditions   map[string]any `json:"conditions,omitempty"`
	Priority     int            `json:"priority"` // higher = evaluated first
}

// Match checks if this policy applies to the given request.
func (p *Policy) Match(resourceType, action string) bool {
	// Wildcard matching
	if p.ResourceType != "" && p.ResourceType != "*" && p.ResourceType != resourceType {
		return false
	}
	if p.Action != "" && p.Action != "*" && p.Action != action {
		return false
	}
	return true
}

// EvaluationRequest contains context for policy evaluation.
type EvaluationRequest struct {
	User         *models.User
	Task         *models.Task
	Workspace    *models.Workspace
	Action       string
	ResourceType string
	Resource     string
	Details      map[string]any
}

// Engine evaluates policies and returns the most restrictive matching effect.
type Engine struct {
	policies []Policy
}

// NewEngine creates a policy engine with the given policies.
func NewEngine(policies []Policy) *Engine {
	return &Engine{policies: policies}
}

// DefaultEngine returns an engine with sensible default policies.
func DefaultEngine() *Engine {
	return NewEngine([]Policy{
		// Allow: non-destructive reads
		{Name: "allow_file_reads", ResourceType: "file", Action: "read", Effect: EffectAllow, Priority: 100},
		{Name: "allow_repo_search", ResourceType: "command", Action: "search", Effect: EffectAllow, Priority: 100},
		{Name: "allow_static_analysis", ResourceType: "command", Action: "analyze", Effect: EffectAllow, Priority: 100},
		{Name: "allow_tests", ResourceType: "command", Action: "test", Effect: EffectAllow, Priority: 100},

		// Ask: mutations and external actions
		{Name: "ask_file_writes", ResourceType: "file", Action: "write", Effect: EffectAsk, Priority: 200},
		{Name: "ask_command_execute", ResourceType: "command", Action: "execute", Effect: EffectAsk, Priority: 200},
		{Name: "ask_dependency_install", ResourceType: "command", Action: "install", Effect: EffectAsk, Priority: 200},
		{Name: "ask_db_migrate", ResourceType: "command", Action: "migrate", Effect: EffectAsk, Priority: 200},
		{Name: "ask_git_commit", ResourceType: "git", Action: "commit", Effect: EffectAsk, Priority: 200},
		{Name: "ask_git_push", ResourceType: "git", Action: "push", Effect: EffectAsk, Priority: 200},
		{Name: "ask_network", ResourceType: "network", Action: "*", Effect: EffectAsk, Priority: 200},
		{Name: "ask_pr_create", ResourceType: "git", Action: "create_pr", Effect: EffectAsk, Priority: 200},

		// Deny: dangerous operations
		{Name: "deny_production_secrets", ResourceType: "secret", Action: "read", Effect: EffectDeny, Priority: 300, Conditions: map[string]any{"scope": "production"}},
		{Name: "deny_destructive_db", ResourceType: "command", Action: "destructive_db", Effect: EffectDeny, Priority: 300},
		{Name: "deny_large_delete", ResourceType: "file", Action: "delete", Effect: EffectDeny, Priority: 300, Conditions: map[string]any{"min_size_mb": 10}},
		{Name: "deny_push_main", ResourceType: "git", Action: "push", Effect: EffectDeny, Priority: 300, Conditions: map[string]any{"branch": "main"}},
		{Name: "deny_production_deploy", ResourceType: "deploy", Action: "*", Effect: EffectDeny, Priority: 300, Conditions: map[string]any{"environment": "production"}},

		// Admin-only: sensitive operations
		{Name: "admin_deploy_prod", ResourceType: "deploy", Action: "*", Effect: EffectAdminOnly, Priority: 400, Conditions: map[string]any{"environment": "production"}},
		{Name: "admin_merge_pr", ResourceType: "git", Action: "merge", Effect: EffectAdminOnly, Priority: 400},
		{Name: "admin_write_secrets", ResourceType: "secret", Action: "write", Effect: EffectAdminOnly, Priority: 400},
		{Name: "admin_rotate_secrets", ResourceType: "secret", Action: "rotate", Effect: EffectAdminOnly, Priority: 400},
		{Name: "admin_modify_policies", ResourceType: "policy", Action: "*", Effect: EffectAdminOnly, Priority: 400},
	})
}

// Evaluate checks all policies and returns the most restrictive effect.
// If no policy matches, returns defaultEffect.
func (e *Engine) Evaluate(ctx context.Context, req EvaluationRequest, defaultEffect Effect) (Effect, error) {
	result := defaultEffect
	matched := false

	for _, policy := range e.policies {
		if !policy.Match(req.ResourceType, req.Action) {
			continue
		}
		// Check conditions if present
		if !checkConditions(policy, req) {
			continue
		}
		if !matched {
			result = policy.Effect
			matched = true
			continue
		}
		if policy.Effect.MoreRestrictiveThan(result) {
			result = policy.Effect
		}
	}

	return result, nil
}

// ActionPair groups a ResourceType with an Action for batch evaluation.
type ActionPair struct {
	ResourceType string
	Action       string
}

// EvaluateMany evaluates multiple actions and returns the most restrictive result.
func (e *Engine) EvaluateMany(ctx context.Context, req EvaluationRequest, actions []ActionPair) (Effect, error) {
	result := EffectAllow
	for _, a := range actions {
		req.ResourceType = a.ResourceType
		req.Action = a.Action
		effect, err := e.Evaluate(ctx, req, EffectAsk)
		if err != nil {
			return EffectDeny, err
		}
		if effect.MoreRestrictiveThan(result) {
			result = effect
		}
	}
	return result, nil
}

// EvaluateWithDetails evaluates policies and returns the matching policy names for diagnostics.
func (e *Engine) EvaluateWithDetails(ctx context.Context, req EvaluationRequest, defaultEffect Effect) (Effect, []string, error) {
	result := defaultEffect
	var matched []string

	for _, policy := range e.policies {
		if !policy.Match(req.ResourceType, req.Action) {
			continue
		}
		if !checkConditions(policy, req) {
			continue
		}
		matched = append(matched, policy.Name)
		if len(matched) == 1 {
			result = policy.Effect
			continue
		}
		if policy.Effect.MoreRestrictiveThan(result) {
			result = policy.Effect
		}
	}

	return result, matched, nil
}

// AddPolicy appends a policy to the engine at runtime.
func (e *Engine) AddPolicy(p Policy) {
	e.policies = append(e.policies, p)
}

// Policies returns a copy of all configured policies.
func (e *Engine) Policies() []Policy {
	result := make([]Policy, len(e.policies))
	copy(result, e.policies)
	return result
}

func checkConditions(policy Policy, req EvaluationRequest) bool {
	if len(policy.Conditions) == 0 {
		return true
	}

	for key, expected := range policy.Conditions {
		actual, exists := req.Details[key]
		if !exists {
			// If the condition key is not in the request details,
			// check common context fields.
			switch key {
			case "scope":
				actual = getScope(req)
			case "branch":
				actual = getBranch(req)
			case "environment":
				actual = getEnvironment(req)
			default:
				return false
			}
		}

		// Numeric comparisons for threshold conditions.
		switch exp := expected.(type) {
		case int:
			act, ok := toInt(actual)
			if !ok || act < exp {
				return false
			}
		case float64:
			act, ok := toFloat(actual)
			if !ok || act < exp {
				return false
			}
		default:
			if fmt.Sprint(actual) != fmt.Sprint(expected) {
				return false
			}
		}
	}
	return true
}

// getScope extracts the scope from the evaluation request.
func getScope(req EvaluationRequest) string {
	if req.Workspace != nil && req.Workspace.RuntimeProvider != "" {
		return req.Workspace.RuntimeProvider
	}
	return ""
}

// getBranch extracts the branch from the evaluation request.
func getBranch(req EvaluationRequest) string {
	if req.Workspace != nil {
		return req.Workspace.Branch
	}
	if req.Task != nil {
		return req.Task.TargetBranch
	}
	return ""
}

// getEnvironment extracts the environment from the evaluation request.
func getEnvironment(req EvaluationRequest) string {
	if req.Task != nil {
		return string(req.Task.RiskLevel)
	}
	return ""
}

// toInt converts a value to int.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// toFloat converts a value to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
