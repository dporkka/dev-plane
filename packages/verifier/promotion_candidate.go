package verifier

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	protocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

// TrustedCandidateRequest must be constructed by a privileged worker after
// independently fetching Git object identity and protected policy bytes.
// It must never be populated directly from an agent or unauthenticated API.
type TrustedCandidateRequest struct {
	SessionID          string
	WorkItemID         string
	Repository         string
	TargetBranch       string
	SourceHeadSHA      string
	TargetBaseSHA      string
	CandidateCommitSHA string
	CandidateTreeSHA   string
	PolicyRevisionSHA  string
	PolicyBytes        []byte
	RunnerDigest       string
	SigningKey         []byte
}

// CandidateRuntime intentionally lacks workspace file reads: policies arrive from the
// protected revision via the privileged orchestrator, never from candidate files.
type CandidateRuntime interface {
	ExecuteCommand(ctx context.Context, sessionID string, cmd runtimes.Command) (*runtimes.CommandResult, error)
}

// CandidateAttemptStore only accepts insert-only concluded attempts.
type CandidateAttemptStore interface {
	InsertVerificationAttempt(ctx context.Context, attempt protocol.VerificationAttempt) error
}

// VerifyTrustedCandidate checks exact Git identity and clean working tree before
// and after each mandatory gate. It stores authenticated *positive or negative*
// evidence, without granting merge authority or modifying the work item state.
func VerifyTrustedCandidate(ctx context.Context, runtime CandidateRuntime, store CandidateAttemptStore, req TrustedCandidateRequest) (protocol.VerificationAttempt, error) {
	if runtime == nil || store == nil {
		return protocol.VerificationAttempt{}, errors.New("trusted runtime and attempt store required")
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.WorkItemID) == "" ||
		strings.TrimSpace(req.Repository) == "" || strings.TrimSpace(req.TargetBranch) == "" ||
		strings.TrimSpace(req.RunnerDigest) == "" || len(req.PolicyBytes) == 0 ||
		req.PolicyRevisionSHA != req.TargetBaseSHA ||
		!gitSHA(req.SourceHeadSHA) || !gitSHA(req.TargetBaseSHA) ||
		!gitSHA(req.CandidateCommitSHA) || !gitSHA(req.CandidateTreeSHA) {
		return protocol.VerificationAttempt{}, errors.New("missing, untrusted, or invalid candidate identity")
	}
	if len(req.SigningKey) < 32 {
		return protocol.VerificationAttempt{}, errors.New("trusted signing key is required")
	}
	cfg, err := protocol.ParseConfigYAML(req.PolicyBytes)
	if err != nil {
		return protocol.VerificationAttempt{}, fmt.Errorf("protected policy invalid: %w", err)
	}
	profiles := make([]string, 0, len(cfg.Verification))
	for name := range cfg.Verification {
		profiles = append(profiles, name)
	}
	sort.Strings(profiles)
	if len(profiles) == 0 {
		return protocol.VerificationAttempt{}, errors.New("protected policy has no executable gates")
	}
	if err := assertCandidateSnapshot(ctx, runtime, req); err != nil {
		return protocol.VerificationAttempt{}, err
	}

	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return protocol.VerificationAttempt{}, err
	}
	hash := sha256.Sum256(req.PolicyBytes)
	a := protocol.VerificationAttempt{
		ID: hex.EncodeToString(entropy[:]), WorkItemID: req.WorkItemID, Repository: req.Repository,
		TargetBranch: req.TargetBranch, SourceHeadSHA: req.SourceHeadSHA,
		TargetBaseSHA: req.TargetBaseSHA, CandidateCommitSHA: req.CandidateCommitSHA,
		CandidateTreeSHA: req.CandidateTreeSHA, PolicyDigest: fmt.Sprintf("sha256:%x", hash),
		RunnerDigest: req.RunnerDigest, RequiredGates: profiles,
		Gates: make([]protocol.VerificationGate, 0, len(profiles)),
	}
	for _, name := range profiles {
		profile := cfg.Verification[name]
		output, runErr := runtime.ExecuteCommand(ctx, req.SessionID, runtimes.Command{
			Command: profile.Command, Timeout: profileTimeout(profile.TimeoutSeconds), UnsafeShell: true,
		})
		// Do not publish evidence if the candidate's identity or tracked/untracked files
		// changed during verification. Only a trusted disposable runner is sufficient:
		// ignored files, mounts and external services remain outside Git's visibility.
		if err := assertCandidateSnapshot(ctx, runtime, req); err != nil {
			return protocol.VerificationAttempt{}, fmt.Errorf("candidate mutated during %s: %w", name, err)
		}
		status := protocol.GatePassed
		if runErr != nil || output == nil || output.ExitCode != 0 {
			status = protocol.GateFailed
		}
		transcript := evidenceOutput(output, runErr)
		logHash := sha256.Sum256([]byte(transcript))
		a.Gates = append(a.Gates, protocol.VerificationGate{
			Name: name, Status: status, SubjectSHA: req.CandidateCommitSHA,
			LogDigest: fmt.Sprintf("sha256:%x", logHash),
		})
		if status == protocol.GateFailed {
			break
		}
	}
	a.CompletedAt = time.Now().UTC()
	signed, err := protocol.SignVerificationAttempt(a, req.SigningKey)
	if err != nil {
		return protocol.VerificationAttempt{}, fmt.Errorf("sign candidate evidence: %w", err)
	}
	if err := store.InsertVerificationAttempt(ctx, signed); err != nil {
		return protocol.VerificationAttempt{}, fmt.Errorf("persist signed candidate evidence: %w", err)
	}
	if err := signed.Passed(profiles); err != nil {
		return signed, fmt.Errorf("verification failed: %w", err)
	}
	return signed, nil
}

func gitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
func candidateGit(ctx context.Context, runtime CandidateRuntime, session string, args ...string) (string, error) {
	res, err := runtime.ExecuteCommand(ctx, session, runtimes.Command{Args: append([]string{"git"}, args...), Timeout: 30 * time.Second})
	if err != nil {
		return "", err
	}
	if res == nil || res.ExitCode != 0 {
		return "", fmt.Errorf("git %s failed", strings.Join(args, " "))
	}
	return strings.TrimSpace(res.Stdout), nil
}
func assertCandidateSnapshot(ctx context.Context, runtime CandidateRuntime, req TrustedCandidateRequest) error {
	head, err := candidateGit(ctx, runtime, req.SessionID, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	tree, err := candidateGit(ctx, runtime, req.SessionID, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	parents, err := candidateGit(ctx, runtime, req.SessionID, "show", "-s", "--format=%P", "HEAD")
	if err != nil {
		return err
	}
	status, err := candidateGit(ctx, runtime, req.SessionID, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if head != req.CandidateCommitSHA || tree != req.CandidateTreeSHA ||
		parents != req.TargetBaseSHA+" "+req.SourceHeadSHA || status != "" {
		return errors.New("candidate commit, tree, merge parents, or clean workspace mismatch")
	}
	return nil
}
