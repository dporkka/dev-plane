package verification

import (
	"errors"
	"fmt"
	"strings"
)

const ArtifactVersion = 1

// Artifact is a self-contained verification record. The embedded contract makes
// affected-validation scope inspectable while Evidence cryptographically binds
// that contract to the verified tree and environment.
type Artifact struct {
	Version  int      `json:"version"`
	Contract Contract `json:"contract"`
	Evidence Evidence `json:"evidence"`
}

// Validate checks that the embedded evidence was created for the exact embedded
// contract and that all required checks are represented as successful results.
func (a Artifact) Validate() error {
	if a.Version != ArtifactVersion {
		return fmt.Errorf("unsupported verification artifact version %d", a.Version)
	}
	contractHash, err := a.Contract.Digest()
	if err != nil {
		return fmt.Errorf("validate artifact contract: %w", err)
	}
	if a.Evidence.Version != ContractVersion {
		return fmt.Errorf("unsupported evidence version %d", a.Evidence.Version)
	}
	if a.Evidence.ContractHash != contractHash {
		return errors.New("verification artifact contract hash does not match evidence")
	}
	if strings.TrimSpace(a.Evidence.TreeHash) == "" {
		return errors.New("verification artifact tree hash is required")
	}
	if strings.TrimSpace(a.Evidence.EnvironmentDigest) == "" {
		return errors.New("verification artifact environment digest is required")
	}
	if strings.TrimSpace(a.Evidence.RunnerIdentity) == "" {
		return errors.New("verification artifact runner identity is required")
	}
	if a.Evidence.StartedAt.IsZero() || a.Evidence.CompletedAt.IsZero() {
		return errors.New("verification artifact timestamps are required")
	}
	if a.Evidence.CompletedAt.Before(a.Evidence.StartedAt) {
		return errors.New("verification artifact completion time precedes start time")
	}

	results := make(map[string]CheckResult, len(a.Evidence.Checks))
	for _, result := range a.Evidence.Checks {
		id := strings.TrimSpace(result.ID)
		if id == "" {
			return errors.New("verification artifact check result id is required")
		}
		if _, exists := results[id]; exists {
			return fmt.Errorf("verification artifact duplicate check result id %q", id)
		}
		results[id] = result
	}
	for _, check := range a.Contract.Checks {
		if !check.Required {
			continue
		}
		result, ok := results[check.ID]
		if !ok {
			return fmt.Errorf("verification artifact missing required check %q", check.ID)
		}
		if !result.Passed {
			return fmt.Errorf("verification artifact required check %q failed", check.ID)
		}
	}
	return nil
}
