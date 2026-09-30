package decisionpacket

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const PacketVersion = 1

type Candidate struct {
	ID            string `json:"id"`
	PullRequestID string `json:"pull_request_id"`
	TaskID        string `json:"task_id"`
	RunID         string `json:"run_id"`
	RepositoryID  string `json:"repository_id"`
	CommitSHA     string `json:"commit_sha"`
	TreeHash      string `json:"tree_hash"`
	Branch        string `json:"branch"`
}

type TaskSnapshot struct {
	Title                string          `json:"title"`
	Description          string          `json:"description,omitempty"`
	AcceptanceCriteria   json.RawMessage `json:"acceptance_criteria,omitempty"`
	ApprovalRequirements json.RawMessage `json:"approval_requirements,omitempty"`
}

type ReviewSnapshot struct {
	Summary       string          `json:"summary,omitempty"`
	RiskLevel     string          `json:"risk_level"`
	Approvable    bool            `json:"approvable"`
	TestCoverage  string          `json:"test_coverage,omitempty"`
	SecurityNotes string          `json:"security_notes,omitempty"`
	Findings      json.RawMessage `json:"findings,omitempty"`
	DiffSummary   json.RawMessage `json:"diff_summary,omitempty"`
}

type VerificationSnapshot struct {
	ContractHash      string          `json:"contract_hash"`
	EnvironmentDigest string          `json:"environment_digest"`
	RunnerIdentity    string          `json:"runner_identity"`
	Checks            json.RawMessage `json:"checks"`
}

type Input struct {
	Candidate    Candidate
	Task         TaskSnapshot
	Review       ReviewSnapshot
	Verification VerificationSnapshot
	CreatedAt    time.Time
}

type Packet struct {
	Version      int                  `json:"version"`
	Candidate    Candidate            `json:"candidate"`
	Task         TaskSnapshot         `json:"task"`
	Review       ReviewSnapshot       `json:"review"`
	Verification VerificationSnapshot `json:"verification"`
	CreatedAt    time.Time            `json:"created_at"`
}

func New(input Input) (Packet, error) {
	task := input.Task
	review := input.Review
	verification := input.Verification

	var err error
	if task.AcceptanceCriteria, err = normalizeRaw(task.AcceptanceCriteria, false); err != nil {
		return Packet{}, fmt.Errorf("acceptance criteria: %w", err)
	}
	if task.ApprovalRequirements, err = normalizeRaw(task.ApprovalRequirements, false); err != nil {
		return Packet{}, fmt.Errorf("approval requirements: %w", err)
	}
	if review.Findings, err = normalizeRaw(review.Findings, false); err != nil {
		return Packet{}, fmt.Errorf("review findings: %w", err)
	}
	if review.DiffSummary, err = normalizeRaw(review.DiffSummary, false); err != nil {
		return Packet{}, fmt.Errorf("diff summary: %w", err)
	}
	if verification.Checks, err = normalizeRaw(verification.Checks, true); err != nil {
		return Packet{}, fmt.Errorf("verification checks: %w", err)
	}

	packet := Packet{
		Version:      PacketVersion,
		Candidate:    input.Candidate,
		Task:         task,
		Review:       review,
		Verification: verification,
		CreatedAt:    input.CreatedAt.UTC(),
	}
	if err := packet.Validate(); err != nil {
		return Packet{}, err
	}
	return packet, nil
}

func (p Packet) Validate() error {
	if p.Version != PacketVersion {
		return fmt.Errorf("unsupported decision packet version %d", p.Version)
	}
	required := []struct {
		name  string
		value string
	}{
		{"candidate id", p.Candidate.ID},
		{"pull request id", p.Candidate.PullRequestID},
		{"task id", p.Candidate.TaskID},
		{"run id", p.Candidate.RunID},
		{"repository id", p.Candidate.RepositoryID},
		{"commit sha", p.Candidate.CommitSHA},
		{"tree hash", p.Candidate.TreeHash},
		{"branch", p.Candidate.Branch},
		{"task title", p.Task.Title},
		{"review risk level", p.Review.RiskLevel},
		{"verification contract hash", p.Verification.ContractHash},
		{"verification environment digest", p.Verification.EnvironmentDigest},
		{"verification runner identity", p.Verification.RunnerIdentity},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("decision packet %s is required", field.name)
		}
	}
	if p.CreatedAt.IsZero() {
		return errors.New("decision packet created_at is required")
	}
	if err := validateRaw(p.Task.AcceptanceCriteria, false); err != nil {
		return fmt.Errorf("acceptance criteria: %w", err)
	}
	if err := validateRaw(p.Task.ApprovalRequirements, false); err != nil {
		return fmt.Errorf("approval requirements: %w", err)
	}
	if err := validateRaw(p.Review.Findings, false); err != nil {
		return fmt.Errorf("review findings: %w", err)
	}
	if err := validateRaw(p.Review.DiffSummary, false); err != nil {
		return fmt.Errorf("diff summary: %w", err)
	}
	if err := validateRaw(p.Verification.Checks, true); err != nil {
		return fmt.Errorf("verification checks: %w", err)
	}
	return nil
}

func (p Packet) Digest() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal decision packet: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (p Packet) Marshal() (json.RawMessage, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal decision packet: %w", err)
	}
	return data, nil
}

func normalizeRaw(raw json.RawMessage, required bool) (json.RawMessage, error) {
	if len(raw) == 0 {
		if required {
			return nil, errors.New("value is required")
		}
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, errors.New("invalid JSON")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if value == nil {
		if required {
			return nil, errors.New("value is required")
		}
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func validateRaw(raw json.RawMessage, required bool) error {
	if len(raw) == 0 || string(raw) == "null" {
		if required {
			return errors.New("value is required")
		}
		return nil
	}
	if !json.Valid(raw) {
		return errors.New("invalid JSON")
	}
	return nil
}
