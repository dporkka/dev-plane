package repointel

import "testing"

func TestRouterSelectsCheapestPreciseBackend(t *testing.T) {
	router := NewRouter()

	tests := []struct {
		name    string
		request RouteRequest
		want    Backend
	}{
		{"exact worktree lookup uses ripgrep", RouteRequest{Intent: IntentLocate, Mode: ModeExact, Scope: ScopeWorktree}, BackendRipgrep},
		{"exact fleet lookup uses zoekt", RouteRequest{Intent: IntentLocate, Mode: ModeExact, Scope: ScopeFleet}, BackendZoekt},
		{"conceptual lookup uses codebase memory", RouteRequest{Intent: IntentLocate, Mode: ModeConceptual, Scope: ScopeFleet}, BackendCodebaseMemory},
		{"symbol understanding uses lsp", RouteRequest{Intent: IntentUnderstand, Mode: ModeSymbol, Scope: ScopeWorktree}, BackendLSP},
		{"architecture understanding uses codebase memory", RouteRequest{Intent: IntentUnderstand, Mode: ModeArchitecture, Scope: ScopeFleet}, BackendCodebaseMemory},
		{"impact uses codebase memory", RouteRequest{Intent: IntentImpact, Mode: ModeSymbol, Scope: ScopeFleet}, BackendCodebaseMemory},
		{"structural refactor uses ast grep", RouteRequest{Intent: IntentRefactor, Mode: ModeStructural, Scope: ScopeWorktree}, BackendASTGrep},
		{"security data flow uses joern", RouteRequest{Intent: IntentSecurity, Mode: ModeDataFlow, Scope: ScopeWorktree}, BackendJoern},
		{"history uses git", RouteRequest{Intent: IntentHistory, Mode: ModeExact, Scope: ScopeWorktree}, BackendGit},
		{"verification uses tests", RouteRequest{Intent: IntentVerify, Mode: ModeExact, Scope: ScopeWorktree}, BackendTests},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := router.Route(tt.request)
			if err != nil {
				t.Fatalf("route: %v", err)
			}
			if plan.Primary != tt.want {
				t.Fatalf("primary = %q, want %q; plan=%#v", plan.Primary, tt.want, plan)
			}
		})
	}
}

func TestRouterAddsSemanticAndExecutableSafetyForImpact(t *testing.T) {
	router := NewRouter()
	plan, err := router.Route(RouteRequest{Intent: IntentImpact, Mode: ModeSymbol, Scope: ScopeFleet})
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	if !containsBackend(plan.Fallbacks, BackendLSP) {
		t.Fatalf("impact fallbacks = %#v, want LSP semantic check", plan.Fallbacks)
	}
	if !containsBackend(plan.VerifyWith, BackendTests) {
		t.Fatalf("impact verification = %#v, want executable tests", plan.VerifyWith)
	}
}

func TestRouterRejectsUnsupportedCombinations(t *testing.T) {
	router := NewRouter()
	_, err := router.Route(RouteRequest{Intent: IntentHistory, Mode: ModeDataFlow, Scope: ScopeFleet})
	if err == nil {
		t.Fatal("expected invalid routing combination to fail")
	}
}

func containsBackend(backends []Backend, want Backend) bool {
	for _, backend := range backends {
		if backend == want {
			return true
		}
	}
	return false
}
