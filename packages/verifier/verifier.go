package verifier

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

const (
	defaultProfileTimeout = 5 * time.Minute
	maxProfileTimeout     = 30 * time.Minute
	maxEvidenceOutput     = 50_000
)

type Runtime interface {
	ReadFile(ctx context.Context, sessionID, path string) ([]byte, error)
	ExecuteCommand(ctx context.Context, sessionID string, cmd runtimes.Command) (*runtimes.CommandResult, error)
}

type EvidenceStore interface {
	PutEvidenceBundle(ctx context.Context, bundle repoprotocol.EvidenceBundle) error
	PutWorkItem(ctx context.Context, item repoprotocol.WorkItem) error
}

type Request struct {
	SessionID     string
	WorkItem      repoprotocol.WorkItem
	CandidateHead string
	ChangedPaths  []string
}

type Result struct {
	Evidence     repoprotocol.EvidenceBundle
	PendingGates []string
	NextState    repoprotocol.WorkState
}

type Verifier struct {
	runtime Runtime
	store   EvidenceStore
}

func New(runtime Runtime, store EvidenceStore) *Verifier {
	return &Verifier{runtime: runtime, store: store}
}

func (v *Verifier) Verify(ctx context.Context, req Request) (Result, error) {
	if v == nil || v.runtime == nil {
		return Result{}, errors.New("verification runtime is required")
	}
	if v.store == nil {
		return Result{}, errors.New("evidence store is required")
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return Result{}, errors.New("session id is required")
	}
	req.CandidateHead = strings.TrimSpace(req.CandidateHead)
	if req.CandidateHead == "" {
		return Result{}, errors.New("candidate head is required")
	}
	if err := req.WorkItem.Validate(); err != nil {
		return Result{}, fmt.Errorf("validate work item: %w", err)
	}

	configBytes, err := v.runtime.ReadFile(ctx, req.SessionID, "devplane.yaml")
	if err != nil {
		return Result{}, fmt.Errorf("read devplane.yaml: %w", err)
	}
	cfg, err := repoprotocol.ParseConfigYAML(configBytes)
	if err != nil {
		return Result{}, err
	}

	executable, pending, err := resolveGates(cfg, req.WorkItem.RequiredGates, req.ChangedPaths)
	if err != nil {
		return Result{}, err
	}

	if err := v.assertHead(ctx, req.SessionID, req.CandidateHead, false); err != nil {
		return Result{}, err
	}

	result := Result{
		Evidence: repoprotocol.EvidenceBundle{
			WorkItemID: req.WorkItem.ID,
			BaseSHA:    req.WorkItem.BaseSHA,
			HeadSHA:    req.CandidateHead,
			Gates:      make([]repoprotocol.GateEvidence, 0, len(executable)),
		},
		PendingGates: pending,
	}

	for _, gateName := range executable {
		profile := cfg.Verification[gateName]
		command := runtimes.Command{
			Command:     profile.Command,
			Timeout:     profileTimeout(profile.TimeoutSeconds),
			UnsafeShell: true,
		}
		commandResult, runErr := v.runtime.ExecuteCommand(ctx, req.SessionID, command)

		if err := v.assertHead(ctx, req.SessionID, req.CandidateHead, true); err != nil {
			return Result{}, err
		}

		evidence := repoprotocol.GateEvidence{
			Name:    gateName,
			Status:  repoprotocol.GatePassed,
			Command: profile.Command,
			Output:  evidenceOutput(commandResult, runErr),
		}
		failed := runErr != nil || commandResult == nil || commandResult.ExitCode != 0
		if failed {
			evidence.Status = repoprotocol.GateFailed
		}
		result.Evidence.Gates = append(result.Evidence.Gates, evidence)

		if failed {
			if err := v.store.PutEvidenceBundle(ctx, result.Evidence); err != nil {
				return Result{}, fmt.Errorf("persist failed verification evidence: %w", err)
			}
			return result, fmt.Errorf("gate %s failed", gateName)
		}
	}

	if len(result.Evidence.Gates) > 0 {
		if err := v.store.PutEvidenceBundle(ctx, result.Evidence); err != nil {
			return Result{}, fmt.Errorf("persist verification evidence: %w", err)
		}
	}

	nextState := repoprotocol.WorkReadyToLand
	if len(result.PendingGates) > 0 {
		nextState = repoprotocol.WorkReviewing
	}
	req.WorkItem.State = nextState
	req.WorkItem.ClaimedBy = ""
	req.WorkItem.LeaseUntil = nil
	if err := v.store.PutWorkItem(ctx, req.WorkItem); err != nil {
		return Result{}, fmt.Errorf("persist post-verification work state: %w", err)
	}
	result.NextState = nextState
	return result, nil
}

func resolveGates(cfg repoprotocol.Config, workGates, changedPaths []string) ([]string, []string, error) {
	required := make([]string, 0, len(workGates)+len(cfg.Risk))
	seenRequired := make(map[string]struct{})
	appendUnique := func(gate string) {
		gate = strings.TrimSpace(gate)
		if gate == "" {
			return
		}
		if _, exists := seenRequired[gate]; exists {
			return
		}
		seenRequired[gate] = struct{}{}
		required = append(required, gate)
	}

	for _, gate := range workGates {
		appendUnique(gate)
	}
	for _, gate := range cfg.RequiredGatesForPaths(changedPaths) {
		appendUnique(gate)
	}

	var executable []string
	var pending []string
	for _, gate := range required {
		switch gate {
		case repoprotocol.GateIndependentReview, repoprotocol.GateHumanApproval:
			pending = append(pending, gate)
		default:
			if _, ok := cfg.Verification[gate]; !ok {
				return nil, nil, fmt.Errorf("unknown required gate %q", gate)
			}
			executable = append(executable, gate)
		}
	}
	return executable, pending, nil
}

func (v *Verifier) assertHead(ctx context.Context, sessionID, candidate string, duringVerification bool) error {
	result, err := v.runtime.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Args:    []string{"git", "rev-parse", "HEAD"},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("read workspace head: %w", err)
	}
	if result == nil || result.ExitCode != 0 {
		return errors.New("read workspace head failed")
	}
	actual := strings.TrimSpace(result.Stdout)
	if actual == candidate {
		return nil
	}
	if duringVerification {
		return fmt.Errorf("head changed during verification: candidate=%s actual=%s", candidate, actual)
	}
	return fmt.Errorf("head mismatch before verification: candidate=%s actual=%s", candidate, actual)
}

func profileTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultProfileTimeout
	}
	timeout := time.Duration(seconds) * time.Second
	if timeout > maxProfileTimeout {
		return maxProfileTimeout
	}
	return timeout
}

func evidenceOutput(result *runtimes.CommandResult, err error) string {
	var parts []string
	if result != nil {
		if stdout := strings.TrimSpace(result.Stdout); stdout != "" {
			parts = append(parts, stdout)
		}
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			parts = append(parts, stderr)
		}
	}
	if err != nil {
		parts = append(parts, "error: "+err.Error())
	}
	output := strings.Join(parts, "\n")
	if len(output) > maxEvidenceOutput {
		return output[:maxEvidenceOutput] + "\n... [output truncated]"
	}
	return output
}
