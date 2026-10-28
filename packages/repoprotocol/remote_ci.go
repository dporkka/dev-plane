package repoprotocol

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// CIState captures observed lifecycle boundaries. A queued or running job
// cannot be promoted to a terminal release verdict.
type CIState string

const (
	CIStateQueued   CIState = "queued"
	CIStateAssigned CIState = "assigned"
	CIStateRunning  CIState = "running"
	CIStateTerminal CIState = "terminal"
)

type CIOutcome string

const (
	CIOutcomePassed                CIOutcome = "passed"
	CIOutcomeTestsFailed           CIOutcome = "tests_failed"
	CIOutcomeInfrastructureFailure CIOutcome = "infrastructure_failed"
	CIOutcomeCancelled             CIOutcome = "cancelled"
)

type CIArtifact struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// RemoteCIRecord represents facts established by a trusted CI attestation
// adapter, not fields taken from a GitHub status or untrusted webhook payload.
type RemoteCIRecord struct {
	Version          int          `json:"version"`
	Provider         string       `json:"provider"`
	Repository       string       `json:"repository"`
	HeadSHA          string       `json:"head_sha"`
	PipelineID       string       `json:"pipeline_id"`
	DefinitionSHA256 string       `json:"definition_sha256"`
	RunID            string       `json:"run_id"`
	JobID            string       `json:"job_id"`
	AttemptID        string       `json:"attempt_id"`
	WorkerID         string       `json:"worker_id,omitempty"`
	State            CIState      `json:"state"`
	Outcome          CIOutcome    `json:"outcome,omitempty"`
	ExitCode         *int         `json:"exit_code,omitempty"`
	QueuedAt         time.Time    `json:"queued_at"`
	AssignedAt       time.Time    `json:"assigned_at,omitempty"`
	StartedAt        time.Time    `json:"started_at,omitempty"`
	FinishedAt       time.Time    `json:"finished_at,omitempty"`
	Artifacts        []CIArtifact `json:"artifacts,omitempty"`
}

type RemoteCIReceipt struct {
	Record      RemoteCIRecord `json:"record"`
	SignerKeyID string         `json:"signer_key_id"`
	Signature   string         `json:"signature"`
}

// RemoteCIPolicy must be supplied by trusted server-side configuration. Never
// load its public key, expected digest, or repository from the receipt itself.
type RemoteCIPolicy struct {
	Provider         string
	Repository       string
	DefinitionSHA256 string
	SignerKeyID      string
	PublicKey        ed25519.PublicKey
}

func validHexDigest(s string, size int) bool {
	if len(s) != size || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validateCIName(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\x00\n\r")
}

func (r RemoteCIRecord) Validate() error {
	if r.Version != 1 {
		return fmt.Errorf("unsupported remote CI version %d", r.Version)
	}
	for _, value := range []string{r.Provider, r.Repository, r.PipelineID, r.RunID, r.JobID, r.AttemptID} {
		if !validateCIName(value) {
			return errors.New("remote CI identity missing or malformed")
		}
	}
	if !(validHexDigest(r.HeadSHA, 40) || validHexDigest(r.HeadSHA, 64)) {
		return errors.New("remote CI source SHA must be a full lowercase Git SHA")
	}
	if !validHexDigest(r.DefinitionSHA256, 64) {
		return errors.New("invalid CI pipeline definition SHA-256")
	}
	if r.QueuedAt.IsZero() {
		return errors.New("missing CI queued timestamp")
	}
	if r.State != CIStateQueued && r.State != CIStateAssigned && r.State != CIStateRunning && r.State != CIStateTerminal {
		return fmt.Errorf("invalid CI job state %q", r.State)
	}
	if r.State == CIStateQueued {
		if r.WorkerID != "" || !r.AssignedAt.IsZero() || !r.StartedAt.IsZero() || !r.FinishedAt.IsZero() {
			return errors.New("queued job cannot claim assignment or execution")
		}
	} else {
		if !validateCIName(r.WorkerID) || r.AssignedAt.IsZero() || r.AssignedAt.Before(r.QueuedAt) {
			return errors.New("remote CI assignment evidence missing or inconsistent")
		}
		if r.State == CIStateAssigned {
			if !r.StartedAt.IsZero() || !r.FinishedAt.IsZero() {
				return errors.New("assigned job cannot claim execution")
			}
		} else {
			if r.StartedAt.IsZero() || r.StartedAt.Before(r.AssignedAt) {
				return errors.New("remote CI start evidence missing or inconsistent")
			}
			if r.State == CIStateRunning {
				if !r.FinishedAt.IsZero() {
					return errors.New("running CI job cannot claim a finish")
				}
			} else if r.FinishedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) {
				return errors.New("remote CI finish evidence missing or inconsistent")
			}
		}
	}
	if r.State != CIStateTerminal {
		if r.Outcome != "" || r.ExitCode != nil || len(r.Artifacts) != 0 {
			return errors.New("nonterminal CI job cannot claim a verdict or artifacts")
		}
		return nil
	}
	switch r.Outcome {
	case CIOutcomePassed:
		if r.ExitCode == nil || *r.ExitCode != 0 {
			return errors.New("passed CI job requires completed zero exit")
		}
	case CIOutcomeTestsFailed:
		if r.ExitCode == nil || *r.ExitCode == 0 {
			return errors.New("failed test CI job requires nonzero exit")
		}
	case CIOutcomeInfrastructureFailure, CIOutcomeCancelled:
		if r.ExitCode != nil {
			return errors.New("non-executable CI failure must not claim an exit code")
		}
	default:
		return fmt.Errorf("invalid terminal CI outcome %q", r.Outcome)
	}
	seen := make(map[string]bool, len(r.Artifacts))
	for _, artifact := range r.Artifacts {
		if !validateCIName(artifact.Name) || path.IsAbs(artifact.Name) ||
			path.Clean(artifact.Name) != artifact.Name || artifact.Name == ".." ||
			strings.HasPrefix(artifact.Name, "../") || seen[artifact.Name] ||
			artifact.SizeBytes < 0 || !validHexDigest(artifact.SHA256, 64) {
			return fmt.Errorf("invalid or duplicated CI artifact %q", artifact.Name)
		}
		seen[artifact.Name] = true
	}
	return nil
}

func (r RemoteCIRecord) ValidateReleaseSuccess(candidateHead string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.HeadSHA != candidateHead {
		return fmt.Errorf("remote CI head mismatch: got %s want %s", r.HeadSHA, candidateHead)
	}
	if r.State != CIStateTerminal || r.Outcome != CIOutcomePassed {
		return errors.New("remote CI evidence is not a terminal passed execution")
	}
	return nil
}

func remoteCIMessage(r RemoteCIReceipt) ([]byte, error) {
	data, err := json.Marshal(struct {
		SignerKeyID string         `json:"signer_key_id"`
		Record      RemoteCIRecord `json:"record"`
	}{r.SignerKeyID, r.Record})
	if err != nil {
		return nil, err
	}
	return append([]byte("dev-plane/remote-ci-attestation/v1\x00"), data...), nil
}

// SignRemoteCIReceipt is for a privileged attestation service after it has
// independently fetched the CI job from an authoritative provider API.
// Signing an unverified status or webhook does not establish CI trust.
func SignRemoteCIReceipt(record RemoteCIRecord, keyID string, privateKey ed25519.PrivateKey) (RemoteCIReceipt, error) {
	if err := record.Validate(); err != nil {
		return RemoteCIReceipt{}, err
	}
	if !validateCIName(keyID) || len(privateKey) != ed25519.PrivateKeySize {
		return RemoteCIReceipt{}, errors.New("valid attestor key and key ID are required")
	}
	r := RemoteCIReceipt{Record: record, SignerKeyID: keyID}
	message, err := remoteCIMessage(r)
	if err != nil {
		return RemoteCIReceipt{}, err
	}
	r.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message))
	return r, nil
}

// Verify checks both authenticity and terminal release eligibility.
func (r RemoteCIReceipt) Verify(candidateHead string, policy RemoteCIPolicy) error {
	if err := r.VerifyObservation(policy); err != nil {
		return err
	}
	return r.Record.ValidateReleaseSuccess(candidateHead)
}

// VerifyObservation validates any signed lifecycle state, including queued and
// failed jobs, without ever treating it as release-eligible success.
func (r RemoteCIReceipt) VerifyObservation(policy RemoteCIPolicy) error {
	if !validateCIName(policy.Provider) || !validateCIName(policy.Repository) ||
		!validHexDigest(policy.DefinitionSHA256, 64) || !validateCIName(policy.SignerKeyID) ||
		len(policy.PublicKey) != ed25519.PublicKeySize {
		return errors.New("trusted remote CI policy is missing or invalid")
	}
	if err := r.Record.Validate(); err != nil {
		return err
	}
	if r.Record.Provider != policy.Provider || r.Record.Repository != policy.Repository ||
		r.Record.DefinitionSHA256 != policy.DefinitionSHA256 || r.SignerKeyID != policy.SignerKeyID {
		return errors.New("remote CI attestation does not match pinned policy")
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(r.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("invalid remote CI signature")
	}
	message, err := remoteCIMessage(r)
	if err != nil {
		return err
	}
	if !ed25519.Verify(policy.PublicKey, message, sig) {
		return errors.New("remote CI attestation signature verification failed")
	}
	return nil
}
