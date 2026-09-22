package vcs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// VerificationReport is the deterministic evidence returned when a candidate
// is replayed onto the current target head.
type VerificationReport struct {
	Passed   bool              `json:"passed"`
	Evidence map[string]string `json:"evidence,omitempty"`
}

// CandidateVerifier re-runs the checks that made a candidate eligible for
// integration. Implementations should be deterministic for the same workspace.
type CandidateVerifier interface {
	Verify(ctx context.Context, workspacePath string) (VerificationReport, error)
}

// VerifyFunc adapts a function into CandidateVerifier.
type VerifyFunc func(context.Context, string) (VerificationReport, error)

func (f VerifyFunc) Verify(ctx context.Context, workspacePath string) (VerificationReport, error) {
	return f(ctx, workspacePath)
}

type MergeStatus string

const (
	MergeStatusMerged            MergeStatus = "merged"
	MergeStatusAlreadyIntegrated MergeStatus = "already_integrated"
	MergeStatusConflict          MergeStatus = "conflict"
	MergeStatusRejected          MergeStatus = "rejected"
	MergeStatusStaleTarget       MergeStatus = "stale_target"
)

var ErrTargetCheckedOut = errors.New("merge target branch is checked out in a worktree")

// MergeRequest submits one immutable verified candidate to a local branch.
type MergeRequest struct {
	RepositoryPath string            `json:"repository_path"`
	TargetRef      string            `json:"target_ref"`
	Candidate      VerifiedCandidate `json:"candidate"`
	Verifier       CandidateVerifier `json:"-"`
	Env            map[string]string `json:"-"`
}

// MergeOutcome is typed so conflicts, policy rejection, and stale-target races
// remain observable rather than being collapsed into generic process errors.
type MergeOutcome struct {
	Status          MergeStatus        `json:"status"`
	TargetRef       string             `json:"target_ref"`
	CandidateCommit string             `json:"candidate_commit"`
	PreviousHead    string             `json:"previous_head,omitempty"`
	NewHead         string             `json:"new_head,omitempty"`
	ActualHead      string             `json:"actual_head,omitempty"`
	Detail          string             `json:"detail,omitempty"`
	Verification    VerificationReport `json:"verification,omitempty"`
}

// MergeRefinery serializes integration per repository/target pair while still
// using update-ref compare-and-swap to protect against writers outside this
// process.
type MergeRefinery struct {
	root     string
	runner   CommandRunner
	recorder Recorder

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

func NewMergeRefinery(root string, runner CommandRunner, recorder Recorder) (*MergeRefinery, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("merge refinery root is required")
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	if recorder == nil {
		recorder = NopRecorder{}
	}
	return &MergeRefinery{
		root:     filepath.Clean(root),
		runner:   runner,
		recorder: recorder,
		locks:    make(map[string]*sync.Mutex),
	}, nil
}

func (r *MergeRefinery) Refine(ctx context.Context, req MergeRequest) (MergeOutcome, error) {
	repository := filepath.Clean(strings.TrimSpace(req.RepositoryPath))
	if strings.TrimSpace(req.RepositoryPath) == "" {
		return MergeOutcome{}, fmt.Errorf("repository path is required")
	}
	if req.Verifier == nil {
		return MergeOutcome{}, fmt.Errorf("candidate verifier is required")
	}
	if strings.TrimSpace(req.Candidate.Revision.CommitID) == "" {
		return MergeOutcome{}, fmt.Errorf("candidate commit ID is required")
	}

	targetRef, err := r.normalizeTargetRef(ctx, repository, req.TargetRef, req.Env)
	if err != nil {
		return MergeOutcome{}, err
	}
	candidateCommit, err := r.resolveCommit(ctx, repository, req.Candidate.Revision.CommitID, req.Env)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("resolve candidate commit: %w", err)
	}

	lock := r.targetLock(repository + "::" + targetRef)
	lock.Lock()
	defer lock.Unlock()

	targetHead, err := r.resolveCommit(ctx, repository, targetRef, req.Env)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("resolve target head: %w", err)
	}
	baseOutcome := MergeOutcome{
		TargetRef:       targetRef,
		CandidateCommit: candidateCommit,
		PreviousHead:    targetHead,
	}

	integrated, err := r.alreadyIntegrated(ctx, repository, targetHead, candidateCommit, req.Env)
	if err != nil {
		return MergeOutcome{}, err
	}
	if integrated {
		baseOutcome.Status = MergeStatusAlreadyIntegrated
		baseOutcome.NewHead = targetHead
		if err := r.recordOutcome(ctx, req, baseOutcome); err != nil {
			return MergeOutcome{}, err
		}
		return baseOutcome, nil
	}

	checkedOut, err := r.targetCheckedOut(ctx, repository, targetRef, req.Env)
	if err != nil {
		return MergeOutcome{}, err
	}
	if checkedOut {
		return MergeOutcome{}, fmt.Errorf("%w: %s", ErrTargetCheckedOut, targetRef)
	}

	worktree := r.refineryWorkspaceRoot(repository, candidateCommit)
	if _, err := r.runner.Run(ctx, Command{
		Name: "mkdir", Args: []string{"-p", filepath.Dir(worktree)}, Dir: repository, Env: req.Env,
	}); err != nil {
		return MergeOutcome{}, fmt.Errorf("create refinery workspace root: %w", err)
	}
	refineryBranch := "dev-plane/refinery/" + filepath.Base(worktree)

	if _, err := r.runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"worktree", "add", "-b", refineryBranch, worktree, targetHead},
		Dir:  repository,
		Env:  req.Env,
	}); err != nil {
		return MergeOutcome{}, fmt.Errorf("create refinery worktree: %w", err)
	}
	defer r.cleanupWorktree(context.Background(), repository, worktree, refineryBranch, req.Env)

	_, pickErr := r.runner.Run(ctx, Command{
		Name: "git",
		Args: []string{
			"-c", "user.name=Dev Plane Refinery",
			"-c", "user.email=refinery@dev-plane.invalid",
			"-c", "commit.gpgsign=false",
			"-c", "core.hooksPath=/dev/null",
			"cherry-pick", "--no-edit", "--no-gpg-sign", candidateCommit,
		},
		Dir: worktree,
		Env: req.Env,
	})
	if pickErr != nil {
		conflicts, inspectErr := r.runner.Run(ctx, Command{
			Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Dir: worktree, Env: req.Env,
		})
		_, _ = r.runner.Run(context.Background(), Command{
			Name: "git", Args: []string{"cherry-pick", "--abort"}, Dir: worktree, Env: req.Env,
		})
		if inspectErr == nil && strings.TrimSpace(conflicts.Stdout) != "" {
			baseOutcome.Status = MergeStatusConflict
			baseOutcome.Detail = strings.TrimSpace(conflicts.Stdout)
			if err := r.recordOutcome(ctx, req, baseOutcome); err != nil {
				return MergeOutcome{}, err
			}
			return baseOutcome, nil
		}
		return MergeOutcome{}, fmt.Errorf("replay candidate: %w", pickErr)
	}

	verification, err := req.Verifier.Verify(ctx, worktree)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("re-verify replayed candidate: %w", err)
	}
	baseOutcome.Verification = verification
	if !verification.Passed {
		baseOutcome.Status = MergeStatusRejected
		if err := r.recordOutcome(ctx, req, baseOutcome); err != nil {
			return MergeOutcome{}, err
		}
		return baseOutcome, nil
	}

	newHead, err := r.resolveCommit(ctx, worktree, "HEAD", req.Env)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("resolve replayed candidate head: %w", err)
	}
	_, updateErr := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"update-ref", targetRef, newHead, targetHead}, Dir: repository, Env: req.Env,
	})
	if updateErr != nil {
		actualHead, resolveErr := r.resolveCommit(ctx, repository, targetRef, req.Env)
		if resolveErr != nil {
			return MergeOutcome{}, fmt.Errorf("update target ref: %w", updateErr)
		}
		if actualHead != targetHead {
			baseOutcome.Status = MergeStatusStaleTarget
			baseOutcome.ActualHead = actualHead
			if err := r.recordOutcome(ctx, req, baseOutcome); err != nil {
				return MergeOutcome{}, err
			}
			return baseOutcome, nil
		}
		return MergeOutcome{}, fmt.Errorf("update target ref: %w", updateErr)
	}

	baseOutcome.Status = MergeStatusMerged
	baseOutcome.NewHead = newHead
	if err := r.recordOutcome(ctx, req, baseOutcome); err != nil {
		return MergeOutcome{}, err
	}
	return baseOutcome, nil
}

func (r *MergeRefinery) refineryWorkspaceRoot(repository, candidateCommit string) string {
	root := r.root
	if !filepath.IsAbs(root) {
		root = filepath.Join(repository, root)
	}
	suffix := strings.TrimSpace(candidateCommit)
	if len(suffix) > 16 {
		suffix = suffix[:16]
	}
	return filepath.Join(root, "candidate-"+suffix)
}

func (r *MergeRefinery) targetLock(key string) *sync.Mutex {
	r.locksMu.Lock()
	defer r.locksMu.Unlock()
	lock := r.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[key] = lock
	}
	return lock
}

func (r *MergeRefinery) normalizeTargetRef(
	ctx context.Context,
	repository, requested string,
	env map[string]string,
) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || strings.HasPrefix(requested, "-") {
		return "", fmt.Errorf("invalid merge target ref %q", requested)
	}
	if strings.HasPrefix(requested, "refs/") && !strings.HasPrefix(requested, "refs/heads/") {
		return "", fmt.Errorf("merge target must be a local branch: %s", requested)
	}
	branch := strings.TrimPrefix(requested, "refs/heads/")
	if _, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"check-ref-format", "--branch", branch}, Dir: repository, Env: env,
	}); err != nil {
		return "", fmt.Errorf("invalid merge target ref %q: %w", requested, err)
	}
	return "refs/heads/" + branch, nil
}

func (r *MergeRefinery) resolveCommit(
	ctx context.Context,
	repository, ref string,
	env map[string]string,
) (string, error) {
	result, err := r.runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"rev-parse", "--verify", "--end-of-options", ref + "^{commit}"},
		Dir:  repository,
		Env:  env,
	})
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(result.Stdout)
	if len(id) != 40 && len(id) != 64 {
		return "", fmt.Errorf("unexpected Git object ID %q", id)
	}
	return id, nil
}

func (r *MergeRefinery) alreadyIntegrated(
	ctx context.Context,
	repository, targetHead, candidateCommit string,
	env map[string]string,
) (bool, error) {
	base, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"merge-base", candidateCommit, targetHead}, Dir: repository, Env: env,
	})
	if err != nil {
		return false, fmt.Errorf("resolve candidate ancestry: %w", err)
	}
	if strings.TrimSpace(base.Stdout) == candidateCommit {
		return true, nil
	}

	cherry, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"cherry", targetHead, candidateCommit}, Dir: repository, Env: env,
	})
	if err != nil {
		return false, fmt.Errorf("check patch equivalence: %w", err)
	}
	for _, line := range strings.Split(cherry.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == candidateCommit && fields[0] == "-" {
			return true, nil
		}
	}
	return false, nil
}

func (r *MergeRefinery) targetCheckedOut(
	ctx context.Context,
	repository, targetRef string,
	env map[string]string,
) (bool, error) {
	result, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"worktree", "list", "--porcelain"}, Dir: repository, Env: env,
	})
	if err != nil {
		return false, fmt.Errorf("list worktrees: %w", err)
	}
	needle := "branch " + targetRef
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == needle {
			return true, nil
		}
	}
	return false, nil
}

func (r *MergeRefinery) cleanupWorktree(
	ctx context.Context,
	repository, worktree, branch string,
	env map[string]string,
) {
	_, _ = r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"worktree", "remove", "--force", worktree}, Dir: repository, Env: env,
	})
	_, _ = r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"branch", "-D", branch}, Dir: repository, Env: env,
	})
	_, _ = r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"worktree", "prune"}, Dir: repository, Env: env,
	})
}

func (r *MergeRefinery) recordOutcome(ctx context.Context, req MergeRequest, outcome MergeOutcome) error {
	attrs := map[string]string{
		"status":     string(outcome.Status),
		"target_ref": outcome.TargetRef,
	}
	if outcome.PreviousHead != "" {
		attrs["previous_head"] = outcome.PreviousHead
	}
	if outcome.NewHead != "" {
		attrs["new_head"] = outcome.NewHead
	}
	if outcome.ActualHead != "" {
		attrs["actual_head"] = outcome.ActualHead
	}
	if outcome.Detail != "" {
		attrs["detail"] = outcome.Detail
	}
	return r.recorder.Record(ctx, ProvenanceEvent{
		Kind:       EventKind("candidate.integration"),
		Backend:    "git",
		TaskID:     req.Candidate.Metadata.TaskID,
		AgentID:    req.Candidate.Metadata.AgentID,
		Workspace:  req.Candidate.Metadata.WorkspaceID,
		PublishRef: outcome.TargetRef,
		CommitID:   outcome.CandidateCommit,
		ChangeID:   req.Candidate.Revision.ChangeID,
		Attributes: attrs,
	})
}
