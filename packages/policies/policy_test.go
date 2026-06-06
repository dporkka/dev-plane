package policies

import (
	"context"
	"testing"

	"github.com/ai-dev-control-plane/models"
)

// TestEffect_MoreRestrictiveThan tests all effect combinations for the restriction ordering.
// Order: allow < ask < deny < admin_only
func TestEffect_MoreRestrictiveThan(t *testing.T) {
	tests := []struct {
		name     string
		e        Effect
		other    Effect
		expected bool
	}{
		// Same effect - never more restrictive
		{"allow vs allow", EffectAllow, EffectAllow, false},
		{"ask vs ask", EffectAsk, EffectAsk, false},
		{"deny vs deny", EffectDeny, EffectDeny, false},
		{"admin_only vs admin_only", EffectAdminOnly, EffectAdminOnly, false},

		// allow is least restrictive
		{"ask vs allow", EffectAsk, EffectAllow, true},
		{"deny vs allow", EffectDeny, EffectAllow, true},
		{"admin_only vs allow", EffectAdminOnly, EffectAllow, true},

		// ask is second
		{"allow vs ask", EffectAllow, EffectAsk, false},
		{"deny vs ask", EffectDeny, EffectAsk, true},
		{"admin_only vs ask", EffectAdminOnly, EffectAsk, true},

		// deny is third
		{"allow vs deny", EffectAllow, EffectDeny, false},
		{"ask vs deny", EffectAsk, EffectDeny, false},
		{"admin_only vs deny", EffectAdminOnly, EffectDeny, true},

		// admin_only is most restrictive
		{"allow vs admin_only", EffectAllow, EffectAdminOnly, false},
		{"ask vs admin_only", EffectAsk, EffectAdminOnly, false},
		{"deny vs admin_only", EffectDeny, EffectAdminOnly, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.e.MoreRestrictiveThan(tt.other)
			if got != tt.expected {
				t.Errorf("%s.MoreRestrictiveThan(%s) = %v, want %v", tt.e, tt.other, got, tt.expected)
			}
		})
	}
}

// TestPolicy_Match tests wildcard and exact matching on resource_type and action.
func TestPolicy_Match(t *testing.T) {
	tests := []struct {
		name         string
		policy       Policy
		resourceType string
		action       string
		expected     bool
	}{
		// Exact match
		{"exact match file:read", Policy{ResourceType: "file", Action: "read"}, "file", "read", true},
		{"exact match command:execute", Policy{ResourceType: "command", Action: "execute"}, "command", "execute", true},

		// Mismatch
		{"wrong resource type", Policy{ResourceType: "file", Action: "read"}, "secret", "read", false},
		{"wrong action", Policy{ResourceType: "file", Action: "read"}, "file", "write", false},
		{"both wrong", Policy{ResourceType: "file", Action: "read"}, "secret", "write", false},

		// Wildcard resource type
		{"wildcard resource type", Policy{ResourceType: "*", Action: "read"}, "anything", "read", true},
		{"wildcard resource type with any action target", Policy{ResourceType: "*", Action: "write"}, "deploy", "write", true},

		// Wildcard action
		{"wildcard action", Policy{ResourceType: "file", Action: "*"}, "file", "anything", true},
		{"wildcard action different", Policy{ResourceType: "deploy", Action: "*"}, "deploy", "execute", true},

		// Both wildcards
		{"both wildcards", Policy{ResourceType: "*", Action: "*"}, "anything", "anything", true},

		// Empty policy fields match anything
		{"empty resource type", Policy{ResourceType: "", Action: "read"}, "file", "read", true},
		{"empty action", Policy{ResourceType: "file", Action: ""}, "file", "read", true},
		{"both empty", Policy{ResourceType: "", Action: ""}, "anything", "anything", true},

		// Specific edge cases
		{"secret read exact", Policy{ResourceType: "secret", Action: "read"}, "secret", "read", true},
		{"git push exact", Policy{ResourceType: "git", Action: "push"}, "git", "push", true},
		{"network wildcard action", Policy{ResourceType: "network", Action: "*"}, "network", "request", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.Match(tt.resourceType, tt.action)
			if got != tt.expected {
				t.Errorf("Match(%q, %q) = %v, want %v", tt.resourceType, tt.action, got, tt.expected)
			}
		})
	}
}

// TestEngine_Evaluate tests the policy engine evaluation with DefaultEngine.
func TestEngine_Evaluate(t *testing.T) {
	ctx := context.Background()
	engine := DefaultEngine()

	tests := []struct {
		name         string
		resourceType string
		action       string
		details      map[string]any
		defaultEff   Effect
		expected     Effect
	}{
		// File read -> allow (use EffectAllow as default so allow policy wins)
		{"file read -> allow", "file", "read", nil, EffectAllow, EffectAllow},

		// File write -> ask (ask is more restrictive than default EffectAllow)
		{"file write -> ask", "file", "write", nil, EffectAllow, EffectAsk},

		// Production secret read -> deny
		{"production secret read -> deny", "secret", "read", map[string]any{"scope": "production"}, EffectAllow, EffectDeny},

		// Non-production secret read -> no deny match (returns default)
		{"non-production secret read -> default", "secret", "read", map[string]any{"scope": "staging"}, EffectAllow, EffectAllow},

		// Production deploy -> admin_only (more restrictive than deny)
		{"production deploy -> admin_only", "deploy", "execute", map[string]any{"environment": "production"}, EffectAllow, EffectAdminOnly},

		// Unknown operation -> default effect
		{"unknown operation -> default", "unknown_resource", "unknown_action", nil, EffectAsk, EffectAsk},

		// Command search -> allow
		{"command search -> allow", "command", "search", nil, EffectAllow, EffectAllow},

		// Command analyze -> allow
		{"command analyze -> allow", "command", "analyze", nil, EffectAllow, EffectAllow},

		// Command test -> allow
		{"command test -> allow", "command", "test", nil, EffectAllow, EffectAllow},

		// Command execute -> ask
		{"command execute -> ask", "command", "execute", nil, EffectAllow, EffectAsk},

		// Command install -> ask
		{"command install -> ask", "command", "install", nil, EffectAllow, EffectAsk},

		// Git commit -> ask
		{"git commit -> ask", "git", "commit", nil, EffectAllow, EffectAsk},

		// Network request -> ask (wildcard action)
		{"network request -> ask", "network", "request", nil, EffectAllow, EffectAsk},

		// Network anything -> ask (wildcard action match)
		{"network anything -> ask", "network", "anything", nil, EffectAllow, EffectAsk},

		// File delete with size condition - large file denied
		{"file delete large -> deny", "file", "delete", map[string]any{"min_size_mb": 20}, EffectAllow, EffectDeny},

		// File delete with size condition - small file passes
		{"file delete small -> default", "file", "delete", map[string]any{"min_size_mb": 5}, EffectAllow, EffectAllow},

		// Git push to main -> deny
		{"git push main -> deny", "git", "push", map[string]any{"branch": "main"}, EffectAllow, EffectDeny},

		// Git push to feature -> ask
		{"git push feature -> ask", "git", "push", map[string]any{"branch": "feature"}, EffectAllow, EffectAsk},

		// Secret read without conditions -> default (no matching unconditional policy)
		{"secret read no conditions -> default", "secret", "read", nil, EffectAsk, EffectAsk},

		// Admin operations
		{"git merge -> admin_only", "git", "merge", nil, EffectAllow, EffectAdminOnly},
		{"secret write -> admin_only", "secret", "write", nil, EffectAllow, EffectAdminOnly},
		{"secret rotate -> admin_only", "secret", "rotate", nil, EffectAllow, EffectAdminOnly},
		{"policy write -> admin_only", "policy", "write", nil, EffectAllow, EffectAdminOnly},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := EvaluationRequest{
				ResourceType: tt.resourceType,
				Action:       tt.action,
				Details:      tt.details,
			}
			got, err := engine.Evaluate(ctx, req, tt.defaultEff)
			if err != nil {
				t.Fatalf("Evaluate() returned error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("Evaluate() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestEngine_EvaluateMany tests evaluating multiple actions with most restrictive winning.
func TestEngine_EvaluateMany(t *testing.T) {
	ctx := context.Background()
	engine := DefaultEngine()

	tests := []struct {
		name     string
		actions  []ActionPair
		expected Effect
	}{
		{
			name: "all reads -> allow",
			actions: []ActionPair{
				{ResourceType: "file", Action: "read"},
				{ResourceType: "command", Action: "search"},
			},
			expected: EffectAllow,
		},
		{
			name: "read and write -> ask",
			actions: []ActionPair{
				{ResourceType: "file", Action: "read"},
				{ResourceType: "file", Action: "write"}, // ask
			},
			expected: EffectAsk,
		},
		{
			name: "allow, ask, no-match -> ask",
			actions: []ActionPair{
				{ResourceType: "file", Action: "read"},
				{ResourceType: "file", Action: "write"},  // ask
				{ResourceType: "secret", Action: "read"}, // no unconditional policy -> default EffectAsk
			},
			expected: EffectAsk,
		},
		{
			name: "deploy no production condition -> ask",
			actions: []ActionPair{
				{ResourceType: "file", Action: "read"},
				{ResourceType: "deploy", Action: "execute"}, // no production condition -> default EffectAsk
			},
			expected: EffectAsk,
		},
		{
			name: "single action search -> allow",
			actions: []ActionPair{
				{ResourceType: "command", Action: "search"},
			},
			expected: EffectAllow,
		},
		{
			name:     "empty actions -> default allow",
			actions:  []ActionPair{},
			expected: EffectAllow, // default starts at EffectAllow, no iterations change it
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := EvaluationRequest{}
			got, err := engine.EvaluateMany(ctx, req, tt.actions)
			if err != nil {
				t.Fatalf("EvaluateMany() returned error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("EvaluateMany() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestEngine_Evaluate_WithWorkspaceConditions tests condition evaluation using workspace context.
func TestEngine_Evaluate_WithWorkspaceConditions(t *testing.T) {
	ctx := context.Background()
	engine := DefaultEngine()

	tests := []struct {
		name      string
		workspace *models.Workspace
		task      *models.Task
		resource  string
		action    string
		expected  Effect
	}{
		{
			name:      "secret read with production workspace provider",
			workspace: &models.Workspace{RuntimeProvider: "production"},
			resource:  "secret",
			action:    "read",
			expected:  EffectDeny,
		},
		{
			name:      "secret read with staging workspace provider -> allow (no matching deny policy)",
			workspace: &models.Workspace{RuntimeProvider: "staging"},
			resource:  "secret",
			action:    "read",
			expected:  EffectAllow, // deny_production_secrets only matches scope=production
		},
		{
			name:      "git push with main branch workspace",
			workspace: &models.Workspace{Branch: "main"},
			resource:  "git",
			action:    "push",
			expected:  EffectDeny,
		},
		{
			name:      "git push with feature branch workspace",
			workspace: &models.Workspace{Branch: "feature/foo"},
			resource:  "git",
			action:    "push",
			expected:  EffectAsk,
		},
		{
			name:      "deploy with production environment condition",
			task:      &models.Task{RiskLevel: models.RiskLevel("production")},
			workspace: nil,
			resource:  "deploy",
			action:    "execute",
			expected:  EffectAdminOnly,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := EvaluationRequest{
				Workspace:    tt.workspace,
				Task:         tt.task,
				ResourceType: tt.resource,
				Action:       tt.action,
			}
			got, err := engine.Evaluate(ctx, req, EffectAllow)
			if err != nil {
				t.Fatalf("Evaluate() returned error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("Evaluate() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestDefaultEngine verifies all default policies are loaded.
func TestDefaultEngine(t *testing.T) {
	engine := DefaultEngine()
	policies := engine.Policies()

	if len(policies) != 22 {
		t.Errorf("DefaultEngine() loaded %d policies, want 22", len(policies))
	}

	// Verify we have the expected policy names
	expectedNames := map[string]bool{
		// Allow policies
		"allow_file_reads":      false,
		"allow_repo_search":     false,
		"allow_static_analysis": false,
		"allow_tests":           false,

		// Ask policies
		"ask_file_writes":        false,
		"ask_command_execute":    false,
		"ask_dependency_install": false,
		"ask_db_migrate":         false,
		"ask_git_commit":         false,
		"ask_git_push":           false,
		"ask_network":            false,
		"ask_pr_create":          false,

		// Deny policies
		"deny_production_secrets": false,
		"deny_destructive_db":     false,
		"deny_large_delete":       false,
		"deny_push_main":          false,
		"deny_production_deploy":  false,

		// Admin-only policies
		"admin_deploy_prod":     false,
		"admin_merge_pr":        false,
		"admin_write_secrets":   false,
		"admin_rotate_secrets":  false,
		"admin_modify_policies": false,
	}

	for _, p := range policies {
		if _, ok := expectedNames[p.Name]; ok {
			expectedNames[p.Name] = true
		} else {
			t.Errorf("unexpected policy name: %s", p.Name)
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("missing expected policy: %s", name)
		}
	}
}

// TestEngine_AddPolicy tests adding a policy at runtime.
func TestEngine_AddPolicy(t *testing.T) {
	engine := NewEngine([]Policy{})
	if len(engine.Policies()) != 0 {
		t.Fatal("expected empty engine")
	}

	p := Policy{Name: "test_policy", ResourceType: "file", Action: "read", Effect: EffectAllow, Priority: 100}
	engine.AddPolicy(p)

	policies := engine.Policies()
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
	if policies[0].Name != "test_policy" {
		t.Errorf("expected policy name 'test_policy', got %s", policies[0].Name)
	}
}

// TestEngine_Policies_ReturnsCopy tests that Policies() returns a copy.
func TestEngine_Policies_ReturnsCopy(t *testing.T) {
	engine := NewEngine([]Policy{
		{Name: "original", ResourceType: "file", Action: "read", Effect: EffectAllow},
	})

	policies := engine.Policies()
	policies[0].Name = "modified"

	original := engine.Policies()
	if original[0].Name != "original" {
		t.Error("Policies() did not return a copy; original was modified")
	}
}

// TestEngine_EvaluateWithDetails tests the detailed evaluation that returns matching policy names.
func TestEngine_EvaluateWithDetails(t *testing.T) {
	ctx := context.Background()
	engine := DefaultEngine()

	tests := []struct {
		name           string
		resourceType   string
		action         string
		expectedEffect Effect
		minMatched     int
	}{
		{"file read matches allow policy", "file", "read", EffectAllow, 1},
		{"file write matches ask policy", "file", "write", EffectAsk, 1},
		{"command search matches allow", "command", "search", EffectAllow, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := EvaluationRequest{
				ResourceType: tt.resourceType,
				Action:       tt.action,
			}
			effect, matched, err := engine.EvaluateWithDetails(ctx, req, EffectAllow)
			if err != nil {
				t.Fatalf("EvaluateWithDetails() returned error: %v", err)
			}
			if effect != tt.expectedEffect {
				t.Errorf("effect = %v, want %v", effect, tt.expectedEffect)
			}
			if len(matched) < tt.minMatched {
				t.Errorf("matched %d policies, expected at least %d", len(matched), tt.minMatched)
			}
		})
	}
}
