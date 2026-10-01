package readiness

import "strings"

// VerificationCommands are deterministic project checks discovered from the
// repository or configured explicitly for a project.
type VerificationCommands struct {
	Test      string
	Lint      string
	Typecheck string
	Build     string
}

// VerificationCheck is one machine-executable verification requirement.
type VerificationCheck struct {
	Name    string
	Command string
}

// VerificationPlan is the canonical ordered set of configured machine checks.
type VerificationPlan struct {
	Checks []VerificationCheck
}

// BuildVerificationPlan converts configured commands into stable evidence names.
// Missing commands stay missing; callers must not invent synthetic checks.
func BuildVerificationPlan(commands VerificationCommands) VerificationPlan {
	candidates := []VerificationCheck{
		{Name: "tests", Command: commands.Test},
		{Name: "lint", Command: commands.Lint},
		{Name: "typecheck", Command: commands.Typecheck},
		{Name: "build", Command: commands.Build},
	}

	checks := make([]VerificationCheck, 0, len(candidates))
	for _, check := range candidates {
		check.Command = strings.TrimSpace(check.Command)
		if check.Command == "" {
			continue
		}
		checks = append(checks, check)
	}
	return VerificationPlan{Checks: checks}
}

// RequiredEvidence returns the stable evidence names required by the plan.
func (p VerificationPlan) RequiredEvidence() []string {
	if len(p.Checks) == 0 {
		return nil
	}
	names := make([]string, 0, len(p.Checks))
	for _, check := range p.Checks {
		names = append(names, check.Name)
	}
	return names
}
