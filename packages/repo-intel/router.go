package repointel

import "fmt"

// Backend identifies a concrete code-intelligence or verification backend.
type Backend string

const (
	BackendRipgrep        Backend = "ripgrep"
	BackendZoekt          Backend = "zoekt"
	BackendLSP            Backend = "lsp"
	BackendASTGrep        Backend = "ast-grep"
	BackendCodebaseMemory Backend = "codebase-memory"
	BackendJoern          Backend = "joern"
	BackendGit            Backend = "git"
	BackendTests          Backend = "tests"
	BackendGitNexus       Backend = "gitnexus"
)

// Intent is the stable, vendor-neutral operation requested by an agent.
type Intent string

const (
	IntentLocate     Intent = "locate"
	IntentUnderstand Intent = "understand"
	IntentImpact     Intent = "impact"
	IntentRefactor   Intent = "refactor"
	IntentSecurity   Intent = "security"
	IntentHistory    Intent = "history"
	IntentVerify     Intent = "verify"
)

// Mode refines an intent enough to route it deterministically.
type Mode string

const (
	ModeExact        Mode = "exact"
	ModeConceptual   Mode = "conceptual"
	ModeSymbol       Mode = "symbol"
	ModeArchitecture Mode = "architecture"
	ModeStructural   Mode = "structural"
	ModeDataFlow     Mode = "data-flow"
)

// Scope identifies whether branch-local or fleet-wide state is required.
type Scope string

const (
	ScopeWorktree Scope = "worktree"
	ScopeFleet    Scope = "fleet"
)

// RouteRequest describes one code-intelligence operation.
type RouteRequest struct {
	Intent Intent
	Mode   Mode
	Scope  Scope
}

// RoutePlan describes the preferred backend, ordered fallbacks, and executable
// verification backends. Callers should not skip VerifyWith for mutating work.
type RoutePlan struct {
	Primary    Backend
	Fallbacks  []Backend
	VerifyWith []Backend
}

// Router maps stable operations to the cheapest precise backend. It is kept
// intentionally deterministic so agent behavior can be tested and audited.
type Router struct{}

func NewRouter() *Router { return &Router{} }

// Route returns the backend plan for a request.
func (r *Router) Route(req RouteRequest) (RoutePlan, error) {
	if req.Scope != ScopeWorktree && req.Scope != ScopeFleet {
		return RoutePlan{}, fmt.Errorf("unsupported scope %q", req.Scope)
	}

	switch req.Intent {
	case IntentLocate:
		switch req.Mode {
		case ModeExact:
			if req.Scope == ScopeFleet {
				return RoutePlan{Primary: BackendZoekt, Fallbacks: []Backend{BackendCodebaseMemory}}, nil
			}
			return RoutePlan{Primary: BackendRipgrep, Fallbacks: []Backend{BackendLSP}}, nil
		case ModeConceptual:
			return RoutePlan{Primary: BackendCodebaseMemory, Fallbacks: []Backend{BackendZoekt}}, nil
		default:
			return RoutePlan{}, unsupported(req)
		}

	case IntentUnderstand:
		switch req.Mode {
		case ModeSymbol:
			return RoutePlan{Primary: BackendLSP, Fallbacks: []Backend{BackendCodebaseMemory}}, nil
		case ModeArchitecture:
			return RoutePlan{Primary: BackendCodebaseMemory, Fallbacks: []Backend{BackendLSP}}, nil
		default:
			return RoutePlan{}, unsupported(req)
		}

	case IntentImpact:
		if req.Mode != ModeSymbol && req.Mode != ModeArchitecture {
			return RoutePlan{}, unsupported(req)
		}
		return RoutePlan{
			Primary:    BackendCodebaseMemory,
			Fallbacks:  []Backend{BackendLSP, BackendGit},
			VerifyWith: []Backend{BackendTests},
		}, nil

	case IntentRefactor:
		if req.Mode != ModeStructural {
			return RoutePlan{}, unsupported(req)
		}
		return RoutePlan{
			Primary:    BackendASTGrep,
			Fallbacks:  []Backend{BackendLSP},
			VerifyWith: []Backend{BackendTests},
		}, nil

	case IntentSecurity:
		if req.Mode != ModeDataFlow {
			return RoutePlan{}, unsupported(req)
		}
		return RoutePlan{
			Primary:    BackendJoern,
			Fallbacks:  []Backend{BackendCodebaseMemory},
			VerifyWith: []Backend{BackendTests},
		}, nil

	case IntentHistory:
		if req.Mode != ModeExact && req.Mode != ModeSymbol {
			return RoutePlan{}, unsupported(req)
		}
		return RoutePlan{Primary: BackendGit}, nil

	case IntentVerify:
		if req.Mode != ModeExact && req.Mode != ModeSymbol && req.Mode != ModeArchitecture {
			return RoutePlan{}, unsupported(req)
		}
		return RoutePlan{Primary: BackendTests}, nil

	default:
		return RoutePlan{}, fmt.Errorf("unsupported intent %q", req.Intent)
	}
}

func unsupported(req RouteRequest) error {
	return fmt.Errorf("unsupported code-intelligence route: intent=%q mode=%q scope=%q", req.Intent, req.Mode, req.Scope)
}
