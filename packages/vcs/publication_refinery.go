package vcs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type PublicationStatus string

const (
	PublicationTargetUnchanged   PublicationStatus = "target_unchanged"
	PublicationPrepared          PublicationStatus = "prepared"
	PublicationAlreadyIntegrated PublicationStatus = "already_integrated"
	PublicationConflict          PublicationStatus = "conflict"
	PublicationRejected          PublicationStatus = "rejected"
)

// VerificationCommand is a trusted deterministic check that must pass after a
// reviewed candidate is replayed onto a newer target head.
type VerificationCommand struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

// CommandVerifier runs trusted project verification commands through the same
// workspace command boundary as the publication refinery.
type CommandVerifier struct {
	runner   CommandRunner
	commands []VerificationCommand
}

func NewCommandVerifier(runner CommandRunner, commands []VerificationCommand) *CommandVerifier {
	if runner == nil {
		runner = ExecRunner{}
	}
	copied := append([]VerificationCommand(nil), commands...)
	return &CommandVerifier{runner: runner, commands: copied}
}

func (v *CommandVerifier) Verify(ctx context.Context, workspacePath string) (VerificationReport, error) {
	evidence := make(map[string]string, len(v.commands))
	if len(v.commands) == 0 {
		return VerificationReport{Passed: false, Evidence: map[string]string{
			"verification": "no deterministic verification commands configured",
		}}, nil
	}
	for i, check := range v.commands {
		command := strings.TrimSpace(check.Command)
		if command == "" {
			continue
		}
		label := strings.TrimSpace(check.Label)
		if label == "" {
			label = fmt.Sprintf("check-%d", i+1)
		}
		_, err := v.runner.Run(ctx, Command{
			Name: "sh",
			Args: []string{"-lc", command},
			Dir:  workspacePath,
		})
		if err != nil {
			if ctx.Err() != nil {
				return VerificationReport{Passed: false, Evidence: evidence}, ctx.Err()
			}
			evidence[label] = "failed"
			return VerificationReport{Passed: false, Evidence: evidence}, nil
		}
		evidence[label] = "passed"
	}
	if len(evidence) == 0 {
		evidence["verification"] = "no non-empty verification commands configured"
		return VerificationReport{Passed: false, Evidence: evidence}, nil
	}
	return VerificationReport{Passed: true, Evidence: evidence}, nil
}

// PublicationRequest prepares one immutable reviewed candidate for publication
// against the current target branch. RemoteURL, when present, must be a trusted
// HTTPS repository URL and is fetched without modifying configured remotes.
type PublicationRequest struct {
	WorkspacePath string            `json:"workspace_path"`
	RemoteURL     string            `json:"remote_url,omitempty"`
	TargetBranch  string            `json:"target_branch"`
	Candidate     VerifiedCandidate `json:"candidate"`
	Verifier      CandidateVerifier `json:"-"`
	Env           map[string]string `json:"-"`
}

type PublicationOutcome struct {
	Status            PublicationStatus  `json:"status"`
	SourceCommit      string             `json:"source_commit"`
	SourceParent      string             `json:"source_parent,omitempty"`
	TargetHead        string             `json:"target_head"`
	PublishedRevision Revision           `json:"published_revision,omitempty"`
	Verification      VerificationReport `json:"verification,omitempty"`
	Detail            string             `json:"detail,omitempty"`
}

// PublicationRefinery replays reviewed candidates onto the latest target
// without moving the target branch. The source workspace must be clean and is
// restored to its original branch/HEAD before Prepare returns.
type PublicationRefinery struct {
	runner   CommandRunner
	recorder Recorder

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

func NewPublicationRefinery(runner CommandRunner, recorder Recorder) *PublicationRefinery {
	if runner == nil {
		runner = ExecRunner{}
	}
	if recorder == nil {
		recorder = NopRecorder{}
	}
	return &PublicationRefinery{
		runner:   runner,
		recorder: recorder,
		locks:    make(map[string]*sync.Mutex),
	}
}

func (r *PublicationRefinery) Prepare(ctx context.Context, req PublicationRequest) (PublicationOutcome, error) {
	workspace := strings.TrimSpace(req.WorkspacePath)
	if workspace == "" {
		return PublicationOutcome{}, fmt.Errorf("workspace path is required")
	}
	targetBranch := strings.TrimSpace(req.TargetBranch)
	if targetBranch == "" || strings.HasPrefix(targetBranch, "-") {
		return PublicationOutcome{}, fmt.Errorf("target branch is required")
	}
	if _, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"check-ref-format", "--branch", targetBranch}, Dir: workspace, Env: req.Env,
	}); err != nil {
		return PublicationOutcome{}, fmt.Errorf("invalid target branch %q: %w", targetBranch, err)
	}
	if strings.TrimSpace(req.Candidate.Revision.CommitID) == "" {
		return PublicationOutcome{}, fmt.Errorf("candidate commit ID is required")
	}

	lock := r.workspaceLock(workspace)
	lock.Lock()
	defer lock.Unlock()

	status, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"status", "--porcelain=v1", "--untracked-files=all"}, Dir: workspace, Env: req.Env,
	})
	if err != nil {
		return PublicationOutcome{}, fmt.Errorf("inspect publication workspace: %w", err)
	}
	if strings.TrimSpace(status.Stdout) != "" {
		return PublicationOutcome{}, errors.New("publication workspace is dirty; refusing to replay reviewed candidate")
	}

	originalHead, err := r.resolveCommit(ctx, workspace, "HEAD", req.Env)
	if err != nil {
		return PublicationOutcome{}, fmt.Errorf("resolve original workspace head: %w", err)
	}
	branchResult, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"branch", "--show-current"}, Dir: workspace, Env: req.Env,
	})
	if err != nil {
		return PublicationOutcome{}, fmt.Errorf("resolve original workspace branch: %w", err)
	}
	originalBranch := strings.TrimSpace(branchResult.Stdout)

	candidateCommit, err := r.resolveCommit(ctx, workspace, req.Candidate.Revision.CommitID, req.Env)
	if err != nil {
		return PublicationOutcome{}, fmt.Errorf("resolve reviewed candidate: %w", err)
	}
	parent, err := r.resolveCommit(ctx, workspace, candidateCommit+"^", req.Env)
	if err != nil {
		return PublicationOutcome{}, fmt.Errorf("resolve reviewed candidate parent: %w", err)
	}

	targetHead, cleanupTarget, err := r.resolveTarget(ctx, workspace, targetBranch, candidateCommit, req.RemoteURL, req.Env)
	if err != nil {
		return PublicationOutcome{}, err
	}
	if cleanupTarget != nil {
		defer cleanupTarget()
	}

	outcome := PublicationOutcome{
		SourceCommit: candidateCommit,
		SourceParent: parent,
		TargetHead:   targetHead,
	}

	integrated, err := r.alreadyIntegrated(ctx, workspace, targetHead, candidateCommit, req.Env)
	if err != nil {
		return PublicationOutcome{}, err
	}
	if integrated {
		outcome.Status = PublicationAlreadyIntegrated
		outcome.PublishedRevision = Revision{CommitID: targetHead, ChangeID: targetHead}
		if err := r.recordOutcome(ctx, req, outcome); err != nil {
			return PublicationOutcome{}, err
		}
		return outcome, nil
	}

	if targetHead == parent {
		outcome.Status = PublicationTargetUnchanged
		outcome.PublishedRevision = req.Candidate.Revision
		outcome.Verification = VerificationReport{
			Passed: true,
			Evidence: map[string]string{
				"target": "unchanged since review",
			},
		}
		if err := r.recordOutcome(ctx, req, outcome); err != nil {
			return PublicationOutcome{}, err
		}
		return outcome, nil
	}

	if req.Verifier == nil {
		outcome.Status = PublicationRejected
		outcome.Detail = "target advanced since review and no deterministic verifier is configured"
		if err := r.recordOutcome(ctx, req, outcome); err != nil {
			return PublicationOutcome{}, err
		}
		return outcome, nil
	}

	if err := r.switchDetached(ctx, workspace, targetHead, req.Env); err != nil {
		return PublicationOutcome{}, fmt.Errorf("switch to target head: %w", err)
	}

	restore := func() error {
		return r.restoreWorkspace(context.Background(), workspace, originalBranch, originalHead, req.Env)
	}
	restored := false
	defer func() {
		if !restored {
			_ = restore()
		}
	}()

	_, pickErr := r.runner.Run(ctx, Command{
		Name: "git",
		Args: []string{
			"-c", "user.name=Dev Plane Refinery",
			"-c", "user.email=refinery@dev-plane.invalid",
			"-c", "commit.gpgsign=false",
			"-c", "core.hooksPath=/dev/null",
			"cherry-pick", "--no-edit", "--no-gpg-sign", candidateCommit,
		},
		Dir: workspace,
		Env: req.Env,
	})
	if pickErr != nil {
		conflicts, inspectErr := r.runner.Run(ctx, Command{
			Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Dir: workspace, Env: req.Env,
		})
		_, _ = r.runner.Run(context.Background(), Command{
			Name: "git",
			Args: []string{"-c", "core.hooksPath=/dev/null", "cherry-pick", "--abort"},
			Dir:  workspace,
			Env:  req.Env,
		})
		if err := restore(); err != nil {
			return PublicationOutcome{}, fmt.Errorf("restore publication workspace after replay failure: %w", err)
		}
		restored = true
		if inspectErr == nil && strings.TrimSpace(conflicts.Stdout) != "" {
			outcome.Status = PublicationConflict
			outcome.Detail = strings.TrimSpace(conflicts.Stdout)
			if err := r.recordOutcome(ctx, req, outcome); err != nil {
				return PublicationOutcome{}, err
			}
			return outcome, nil
		}
		return PublicationOutcome{}, fmt.Errorf("replay reviewed candidate: %w", pickErr)
	}

	verification, verifyErr := req.Verifier.Verify(ctx, workspace)
	outcome.Verification = verification
	if verifyErr != nil {
		if err := restore(); err != nil {
			return PublicationOutcome{}, fmt.Errorf("restore publication workspace after verifier error: %w", err)
		}
		restored = true
		return PublicationOutcome{}, fmt.Errorf("re-verify replayed candidate: %w", verifyErr)
	}
	if !verification.Passed {
		outcome.Status = PublicationRejected
		outcome.Detail = "replayed candidate failed deterministic verification"
		if err := restore(); err != nil {
			return PublicationOutcome{}, fmt.Errorf("restore publication workspace after rejection: %w", err)
		}
		restored = true
		if err := r.recordOutcome(ctx, req, outcome); err != nil {
			return PublicationOutcome{}, err
		}
		return outcome, nil
	}

	newHead, err := r.resolveCommit(ctx, workspace, "HEAD", req.Env)
	if err != nil {
		return PublicationOutcome{}, fmt.Errorf("resolve replayed publication head: %w", err)
	}
	outcome.Status = PublicationPrepared
	outcome.PublishedRevision = Revision{CommitID: newHead, ChangeID: newHead}

	if err := restore(); err != nil {
		return PublicationOutcome{}, fmt.Errorf("restore publication workspace: %w", err)
	}
	restored = true
	if err := r.recordOutcome(ctx, req, outcome); err != nil {
		return PublicationOutcome{}, err
	}
	return outcome, nil
}

func (r *PublicationRefinery) workspaceLock(key string) *sync.Mutex {
	r.locksMu.Lock()
	defer r.locksMu.Unlock()
	lock := r.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[key] = lock
	}
	return lock
}

func (r *PublicationRefinery) resolveTarget(
	ctx context.Context,
	workspace, targetBranch, candidateCommit, remoteURL string,
	env map[string]string,
) (string, func(), error) {
	if strings.TrimSpace(remoteURL) == "" {
		head, err := r.resolveCommit(ctx, workspace, "refs/heads/"+targetBranch, env)
		if err != nil {
			return "", nil, fmt.Errorf("resolve local target branch: %w", err)
		}
		return head, nil, nil
	}
	if err := validatePublishRemoteURL(remoteURL); err != nil {
		return "", nil, err
	}

	sum := sha256.Sum256([]byte(targetBranch + "|" + candidateCommit))
	targetRef := "refs/dev-plane/targets/" + hex.EncodeToString(sum[:12])
	_, err := r.runner.Run(ctx, Command{
		Name: "git",
		Args: []string{
			"-c", "core.hooksPath=/dev/null",
			"-c", "credential.helper=",
			"-c", "core.askPass=",
			"-c", "http.sslVerify=true",
			"fetch", "--no-tags", "--force", "--",
			remoteURL,
			"refs/heads/" + targetBranch + ":" + targetRef,
		},
		Dir: workspace,
		Env: env,
	})
	if err != nil {
		return "", nil, fmt.Errorf("fetch trusted target branch: %w", err)
	}
	cleanup := func() {
		_, _ = r.runner.Run(context.Background(), Command{
			Name: "git", Args: []string{"update-ref", "-d", targetRef}, Dir: workspace, Env: env,
		})
	}
	head, err := r.resolveCommit(ctx, workspace, targetRef, env)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("resolve fetched target head: %w", err)
	}
	return head, cleanup, nil
}

func (r *PublicationRefinery) switchDetached(ctx context.Context, workspace, commit string, env map[string]string) error {
	_, err := r.runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"-c", "core.hooksPath=/dev/null", "switch", "--detach", commit},
		Dir:  workspace,
		Env:  env,
	})
	return err
}

func (r *PublicationRefinery) restoreWorkspace(
	ctx context.Context,
	workspace, branch, head string,
	env map[string]string,
) error {
	if _, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"reset", "--hard", "HEAD"}, Dir: workspace, Env: env,
	}); err != nil {
		return fmt.Errorf("reset publication workspace: %w", err)
	}
	if _, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"clean", "-fd"}, Dir: workspace, Env: env,
	}); err != nil {
		return fmt.Errorf("clean publication workspace: %w", err)
	}
	args := []string{"-c", "core.hooksPath=/dev/null", "switch"}
	if strings.TrimSpace(branch) == "" {
		args = append(args, "--detach", head)
	} else {
		args = append(args, branch)
	}
	if _, err := r.runner.Run(ctx, Command{Name: "git", Args: args, Dir: workspace, Env: env}); err != nil {
		return err
	}
	restored, err := r.resolveCommit(ctx, workspace, "HEAD", env)
	if err != nil {
		return err
	}
	if restored != head {
		return fmt.Errorf("restored workspace HEAD %s, expected %s", restored, head)
	}
	return nil
}

func (r *PublicationRefinery) resolveCommit(
	ctx context.Context,
	workspace, ref string,
	env map[string]string,
) (string, error) {
	result, err := r.runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"rev-parse", "--verify", "--end-of-options", ref + "^{commit}"},
		Dir:  workspace,
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

func (r *PublicationRefinery) alreadyIntegrated(
	ctx context.Context,
	workspace, targetHead, candidateCommit string,
	env map[string]string,
) (bool, error) {
	base, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"merge-base", candidateCommit, targetHead}, Dir: workspace, Env: env,
	})
	if err != nil {
		return false, fmt.Errorf("resolve publication candidate ancestry: %w", err)
	}
	if strings.TrimSpace(base.Stdout) == candidateCommit {
		return true, nil
	}
	cherry, err := r.runner.Run(ctx, Command{
		Name: "git", Args: []string{"cherry", targetHead, candidateCommit}, Dir: workspace, Env: env,
	})
	if err != nil {
		return false, fmt.Errorf("check publication patch equivalence: %w", err)
	}
	for _, line := range strings.Split(cherry.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == candidateCommit && fields[0] == "-" {
			return true, nil
		}
	}
	return false, nil
}

func (r *PublicationRefinery) recordOutcome(ctx context.Context, req PublicationRequest, outcome PublicationOutcome) error {
	attrs := map[string]string{
		"status":        string(outcome.Status),
		"source_commit": outcome.SourceCommit,
		"source_parent": outcome.SourceParent,
		"target_head":   outcome.TargetHead,
	}
	if outcome.PublishedRevision.CommitID != "" {
		attrs["published_commit"] = outcome.PublishedRevision.CommitID
	}
	if outcome.Detail != "" {
		attrs["detail"] = outcome.Detail
	}
	return r.recorder.Record(ctx, ProvenanceEvent{
		Kind:       EventKind("candidate.publication_refined"),
		Backend:    "git",
		TaskID:     req.Candidate.Metadata.TaskID,
		AgentID:    req.Candidate.Metadata.AgentID,
		Workspace:  req.Candidate.Metadata.WorkspaceID,
		PublishRef: req.TargetBranch,
		CommitID:   outcome.PublishedRevision.CommitID,
		ChangeID:   outcome.PublishedRevision.ChangeID,
		Attributes: attrs,
	})
}
