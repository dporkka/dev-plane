package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const RunManifestVersion = 1

type RunResourceLimits struct {
	CPUMillis       int `json:"cpu_millis"`
	MemoryMB        int `json:"memory_mb"`
	DiskMB          int `json:"disk_mb"`
	WallTimeSeconds int `json:"wall_time_seconds"`
}

type RunAuthority struct {
	Network    bool     `json:"network"`
	Secrets    []string `json:"secrets,omitempty"`
	Operations []string `json:"operations"`
}

type RunBudgetLimits struct {
	MaxCostUSD        *float64 `json:"max_cost_usd,omitempty"`
	MaxRuntimeMinutes int      `json:"max_runtime_minutes,omitempty"`
	MaxModelCalls     int      `json:"max_model_calls,omitempty"`
	MaxToolCalls      int      `json:"max_tool_calls,omitempty"`
	MaxShellCommands  int      `json:"max_shell_commands,omitempty"`
}

type RunManifestInput struct {
	RunID              string
	TaskID             string
	RepositoryID       string
	BaseSHA            string
	AgentRole          string
	ExecutionClass     string
	RuntimeProvider    string
	Resources          RunResourceLimits
	Authority          RunAuthority
	Budget             RunBudgetLimits
	RequiredGates      []string
	AcceptanceCriteria []string
}

type RunManifest struct {
	Version            int               `json:"version"`
	RunID              string            `json:"run_id"`
	TaskID             string            `json:"task_id"`
	RepositoryID       string            `json:"repository_id"`
	BaseSHA            string            `json:"base_sha"`
	AgentRole          string            `json:"agent_role"`
	ExecutionClass     string            `json:"execution_class"`
	RuntimeProvider    string            `json:"runtime_provider"`
	Resources          RunResourceLimits `json:"resources"`
	Authority          RunAuthority      `json:"authority"`
	Budget             RunBudgetLimits   `json:"budget"`
	RequiredGates      []string          `json:"required_gates,omitempty"`
	AcceptanceCriteria []string          `json:"acceptance_criteria,omitempty"`
	Digest             string            `json:"digest"`
}

type runManifestDigestPayload struct {
	Version            int               `json:"version"`
	RunID              string            `json:"run_id"`
	TaskID             string            `json:"task_id"`
	RepositoryID       string            `json:"repository_id"`
	BaseSHA            string            `json:"base_sha"`
	AgentRole          string            `json:"agent_role"`
	ExecutionClass     string            `json:"execution_class"`
	RuntimeProvider    string            `json:"runtime_provider"`
	Resources          RunResourceLimits `json:"resources"`
	Authority          RunAuthority      `json:"authority"`
	Budget             RunBudgetLimits   `json:"budget"`
	RequiredGates      []string          `json:"required_gates,omitempty"`
	AcceptanceCriteria []string          `json:"acceptance_criteria,omitempty"`
}

func NewRunManifest(input RunManifestInput) (RunManifest, error) {
	manifest := RunManifest{
		Version:         RunManifestVersion,
		RunID:           strings.TrimSpace(input.RunID),
		TaskID:          strings.TrimSpace(input.TaskID),
		RepositoryID:    strings.TrimSpace(input.RepositoryID),
		BaseSHA:         strings.TrimSpace(input.BaseSHA),
		AgentRole:       strings.TrimSpace(input.AgentRole),
		ExecutionClass:  strings.TrimSpace(input.ExecutionClass),
		RuntimeProvider: strings.TrimSpace(input.RuntimeProvider),
		Resources:       input.Resources,
		Authority: RunAuthority{
			Network:    input.Authority.Network,
			Secrets:    normalizedSet(input.Authority.Secrets),
			Operations: normalizedSet(input.Authority.Operations),
		},
		Budget:             input.Budget,
		RequiredGates:      normalizedSet(input.RequiredGates),
		AcceptanceCriteria: append([]string(nil), input.AcceptanceCriteria...),
	}
	for i := range manifest.AcceptanceCriteria {
		manifest.AcceptanceCriteria[i] = strings.TrimSpace(manifest.AcceptanceCriteria[i])
	}

	if err := manifest.validateWithoutDigest(); err != nil {
		return RunManifest{}, err
	}
	digest, err := manifest.computeDigest()
	if err != nil {
		return RunManifest{}, err
	}
	manifest.Digest = digest
	return manifest, nil
}

func (m RunManifest) VerifyDigest() error {
	if err := m.validateWithoutDigest(); err != nil {
		return err
	}
	if strings.TrimSpace(m.Digest) == "" {
		return errors.New("run manifest digest is required")
	}
	expected, err := m.computeDigest()
	if err != nil {
		return err
	}
	if m.Digest != expected {
		return fmt.Errorf("run manifest digest mismatch: got=%s expected=%s", m.Digest, expected)
	}
	return nil
}

func (m RunManifest) validateWithoutDigest() error {
	if m.Version != RunManifestVersion {
		return fmt.Errorf("unsupported run manifest version %d", m.Version)
	}
	for name, value := range map[string]string{
		"run_id":           m.RunID,
		"task_id":          m.TaskID,
		"repository_id":    m.RepositoryID,
		"base_sha":         m.BaseSHA,
		"agent_role":       m.AgentRole,
		"execution_class":  m.ExecutionClass,
		"runtime_provider": m.RuntimeProvider,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("run manifest %s is required", name)
		}
	}
	if m.Resources.CPUMillis <= 0 {
		return errors.New("run manifest cpu_millis must be positive")
	}
	if m.Resources.MemoryMB <= 0 {
		return errors.New("run manifest memory_mb must be positive")
	}
	if m.Resources.DiskMB <= 0 {
		return errors.New("run manifest disk_mb must be positive")
	}
	if m.Resources.WallTimeSeconds <= 0 {
		return errors.New("run manifest wall_time_seconds must be positive")
	}
	if len(m.Authority.Operations) == 0 {
		return errors.New("run manifest operations must not be empty")
	}
	return nil
}

func (m RunManifest) computeDigest() (string, error) {
	payload := runManifestDigestPayload{
		Version:         m.Version,
		RunID:           m.RunID,
		TaskID:          m.TaskID,
		RepositoryID:    m.RepositoryID,
		BaseSHA:         m.BaseSHA,
		AgentRole:       m.AgentRole,
		ExecutionClass:  m.ExecutionClass,
		RuntimeProvider: m.RuntimeProvider,
		Resources:       m.Resources,
		Authority: RunAuthority{
			Network:    m.Authority.Network,
			Secrets:    normalizedSet(m.Authority.Secrets),
			Operations: normalizedSet(m.Authority.Operations),
		},
		Budget:             m.Budget,
		RequiredGates:      normalizedSet(m.RequiredGates),
		AcceptanceCriteria: append([]string(nil), m.AcceptanceCriteria...),
	}
	for i := range payload.AcceptanceCriteria {
		payload.AcceptanceCriteria[i] = strings.TrimSpace(payload.AcceptanceCriteria[i])
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal run manifest: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func normalizedSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}
