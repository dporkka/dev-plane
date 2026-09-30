package verification

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const ContractVersion = 1

// Environment describes deterministic repository setup required before checks run.
type Environment struct {
	Setup string `json:"setup,omitempty"`
}

// Check defines one deterministic verification command.
type Check struct {
	ID             string `json:"id"`
	Command        string `json:"command"`
	Required       bool   `json:"required,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// Contract is the repository-owned definition of what must be verified before a
// candidate can be trusted.
type Contract struct {
	Version        int         `json:"version"`
	Environment    Environment `json:"environment,omitempty"`
	Checks         []Check     `json:"checks,omitempty"`
	ProtectedPaths []string    `json:"protected_paths,omitempty"`
	Artifacts      []string    `json:"artifacts,omitempty"`
}

// ParseContract parses a strict JSON verification contract.
func ParseContract(data []byte) (Contract, error) {
	var contract Contract

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		return Contract{}, fmt.Errorf("decode verification contract: %w", err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Contract{}, errors.New("decode verification contract: trailing JSON value")
		}
		return Contract{}, fmt.Errorf("decode verification contract: %w", err)
	}

	if err := contract.Validate(); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

// Validate checks structural invariants that make a contract safe to hash and
// evaluate deterministically.
func (c Contract) Validate() error {
	if c.Version != ContractVersion {
		return fmt.Errorf("unsupported verification contract version %d", c.Version)
	}

	seenChecks := make(map[string]struct{}, len(c.Checks))
	for i, check := range c.Checks {
		check.ID = strings.TrimSpace(check.ID)
		if check.ID == "" {
			return fmt.Errorf("check %d: id is required", i)
		}
		if _, exists := seenChecks[check.ID]; exists {
			return fmt.Errorf("duplicate check id %q", check.ID)
		}
		seenChecks[check.ID] = struct{}{}

		if strings.TrimSpace(check.Command) == "" {
			return fmt.Errorf("check %q: command is required", check.ID)
		}
		if check.TimeoutSeconds < 0 {
			return fmt.Errorf("check %q: timeout_seconds cannot be negative", check.ID)
		}
	}

	return nil
}

// Digest returns a deterministic SHA-256 digest of the validated contract.
// Contract fields intentionally avoid maps so standard JSON encoding is a
// stable canonical representation for this version.
func (c Contract) Digest() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode verification contract: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// CheckResult records the outcome of one contract check.
type CheckResult struct {
	ID       string `json:"id"`
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exit_code"`
}

// EvidenceInput contains the identities and results that verification evidence
// must bind together.
type EvidenceInput struct {
	TreeHash          string
	Contract          Contract
	EnvironmentDigest string
	RunnerIdentity    string
	Checks            []CheckResult
	StartedAt         time.Time
	CompletedAt       time.Time
}

// Evidence proves which repository tree was checked under which contract and
// environment. If any identity changes, the evidence is stale.
type Evidence struct {
	Version           int           `json:"version"`
	TreeHash          string        `json:"tree_hash"`
	ContractHash      string        `json:"contract_hash"`
	EnvironmentDigest string        `json:"environment_digest"`
	RunnerIdentity    string        `json:"runner_identity"`
	Checks            []CheckResult `json:"checks"`
	StartedAt         time.Time     `json:"started_at"`
	CompletedAt       time.Time     `json:"completed_at"`
}

// NewEvidence validates required checks and creates evidence bound to the exact
// candidate tree, contract, and environment.
func NewEvidence(input EvidenceInput) (Evidence, error) {
	treeHash := strings.TrimSpace(input.TreeHash)
	if treeHash == "" {
		return Evidence{}, errors.New("tree hash is required")
	}
	environmentDigest := strings.TrimSpace(input.EnvironmentDigest)
	if environmentDigest == "" {
		return Evidence{}, errors.New("environment digest is required")
	}
	runnerIdentity := strings.TrimSpace(input.RunnerIdentity)
	if runnerIdentity == "" {
		return Evidence{}, errors.New("runner identity is required")
	}
	if input.StartedAt.IsZero() {
		return Evidence{}, errors.New("verification start time is required")
	}
	if input.CompletedAt.IsZero() {
		return Evidence{}, errors.New("verification completion time is required")
	}
	if input.CompletedAt.Before(input.StartedAt) {
		return Evidence{}, errors.New("verification completion time precedes start time")
	}

	contractHash, err := input.Contract.Digest()
	if err != nil {
		return Evidence{}, err
	}

	results := make(map[string]CheckResult, len(input.Checks))
	for _, result := range input.Checks {
		id := strings.TrimSpace(result.ID)
		if id == "" {
			return Evidence{}, errors.New("check result id is required")
		}
		if _, exists := results[id]; exists {
			return Evidence{}, fmt.Errorf("duplicate check result id %q", id)
		}
		result.ID = id
		results[id] = result
	}

	for _, check := range input.Contract.Checks {
		if !check.Required {
			continue
		}
		result, exists := results[check.ID]
		if !exists {
			return Evidence{}, fmt.Errorf("required check %q has no result", check.ID)
		}
		if !result.Passed {
			return Evidence{}, fmt.Errorf("required check %q failed", check.ID)
		}
	}

	checks := append([]CheckResult(nil), input.Checks...)
	return Evidence{
		Version:           ContractVersion,
		TreeHash:          treeHash,
		ContractHash:      contractHash,
		EnvironmentDigest: environmentDigest,
		RunnerIdentity:    runnerIdentity,
		Checks:            checks,
		StartedAt:         input.StartedAt,
		CompletedAt:       input.CompletedAt,
	}, nil
}

// FreshFor reports whether evidence still matches the exact candidate,
// verification contract, and environment.
func (e Evidence) FreshFor(treeHash string, contract Contract, environmentDigest string) (bool, string, error) {
	contractHash, err := contract.Digest()
	if err != nil {
		return false, "", err
	}

	if e.TreeHash != strings.TrimSpace(treeHash) {
		return false, "tree hash changed", nil
	}
	if e.ContractHash != contractHash {
		return false, "verification contract changed", nil
	}
	if e.EnvironmentDigest != strings.TrimSpace(environmentDigest) {
		return false, "environment changed", nil
	}
	return true, "", nil
}
