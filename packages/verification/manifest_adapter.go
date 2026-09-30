package verification

import (
	"errors"
	"fmt"

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
