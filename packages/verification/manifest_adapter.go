package verification

import (
	"errors"
	"fmt"
	"sort"

	repomanifest "github.com/ai-dev-control-plane/repo-manifest"
)

// ContractFromManifest derives the deterministic verification contract from a
// repository-owned Dev Plane manifest. Development commands are intentionally
// excluded because they do not prove a candidate is safe to accept.
func ContractFromManifest(manifest *repomanifest.Manifest) (Contract, error) {
	if manifest == nil {
		return Contract{}, errors.New("repository manifest is required")
	}
	if err := manifest.Validate(); err != nil {
		return Contract{}, fmt.Errorf("validate repository manifest: %w", err)
	}

	type declaredCheck struct {
		id      string
		command *repomanifest.Command
	}
	declared := []declaredCheck{
		{id: "lint", command: manifest.Commands.Lint},
		{id: "typecheck", command: manifest.Commands.Typecheck},
		{id: "test", command: manifest.Commands.Test},
		{id: "build", command: manifest.Commands.Build},
	}

	required := make(map[string]struct{}, len(manifest.Validation.FallbackChecks))
	if len(manifest.Validation.FallbackChecks) > 0 {
		for _, id := range manifest.Validation.FallbackChecks {
			required[id] = struct{}{}
		}
	} else {
		for _, item := range declared {
			if item.command != nil {
				required[item.id] = struct{}{}
			}
		}
	}

	checks := make([]Check, 0, len(declared))
	for _, item := range declared {
		if item.command == nil {
			continue
		}
		_, isRequired := required[item.id]
		checks = append(checks, Check{
			ID:             item.id,
			Command:        item.command.Run,
			Required:       isRequired,
			TimeoutSeconds: item.command.TimeoutSeconds,
		})
	}
	if len(checks) == 0 {
		return Contract{}, errors.New("repository manifest declares no validation commands")
	}

	contract := Contract{
		Version: ContractVersion,
		Checks:  checks,
	}
	if err := contract.Validate(); err != nil {
		return Contract{}, fmt.Errorf("derive verification contract: %w", err)
	}
	return contract, nil
}


// ContractFromPlan derives the verification contract for one concrete
// affected-validation plan. Every planned check is required because the plan
// already represents the repository's minimum safe check set for that change.
func ContractFromPlan(plan repomanifest.CheckPlan) (Contract, error) {
	if len(plan.Checks) == 0 {
		return Contract{}, errors.New("validation plan declares no checks")
	}

	checks := make([]Check, 0, len(plan.Checks))
	for _, planned := range plan.Checks {
		id := planned.Kind
		if planned.Component != "" {
			id = planned.Component + ":" + planned.Kind
		}
		checks = append(checks, Check{
			ID:             id,
			Command:        planned.Command.Run,
			Required:       true,
			TimeoutSeconds: planned.Command.TimeoutSeconds,
		})
	}
	sort.Slice(checks, func(i, j int) bool {
		if checks[i].ID != checks[j].ID {
			return checks[i].ID < checks[j].ID
		}
		return checks[i].Command < checks[j].Command
	})

	contract := Contract{
		Version: ContractVersion,
		Checks:  checks,
		Scope: &VerificationScope{
			ChangedFiles:       canonicalStrings(plan.ChangedFiles),
			ChangedComponents:  canonicalStrings(plan.ChangedComponents),
			AffectedComponents: canonicalStrings(plan.AffectedComponents),
		},
	}
	if err := contract.Validate(); err != nil {
		return Contract{}, fmt.Errorf("derive planned verification contract: %w", err)
	}
	return contract, nil
}

func canonicalStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	compacted := out[:0]
	for _, value := range out {
		if len(compacted) == 0 || compacted[len(compacted)-1] != value {
			compacted = append(compacted, value)
		}
	}
	return compacted
}
