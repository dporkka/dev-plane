package verifier

import (
	"context"
	"errors"
	"fmt"
	"strings"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

// WorkspaceRuntime is the minimal runtime surface required to execute one
// verification request in an isolated, disposable workspace.
type WorkspaceRuntime interface {
	Runtime
	CreateWorkspace(ctx context.Context, req runtimes.CreateRequest) (*runtimes.Session, error)
	DestroyWorkspace(ctx context.Context, sessionID string) error
}

// RunRequest describes one isolated verification attempt. Workspace creation is
// deliberately provider-neutral: Docker, remote providers, and Nulang Cloud can
// all satisfy the same lifecycle while the repository-owned devplane.yaml keeps
// verification policy outside the CI scheduler.
type RunRequest struct {
	Workspace     runtimes.CreateRequest
	WorkItem      repoprotocol.WorkItem
	CandidateHead string
	ChangedPaths  []string
}

// RunResult keeps the runtime identity alongside the canonical verification
// result so callers can correlate provider logs/usage without making the
// workspace lifetime externally owned.
type RunResult struct {
	SessionID    string
	Verification Result
}

// Runner owns the complete lifecycle of an ephemeral verification workspace.
// The workspace is always destroyed after verification, including gate failure.
type Runner struct {
	runtime  WorkspaceRuntime
	verifier *Verifier
}

func NewRunner(runtime WorkspaceRuntime, store EvidenceStore) *Runner {
	return &Runner{
		runtime:  runtime,
		verifier: New(runtime, store),
	}
}

func (r *Runner) Run(ctx context.Context, req RunRequest) (result RunResult, err error) {
	if r == nil || r.runtime == nil || r.verifier == nil {
		return RunResult{}, errors.New("verification workspace runtime is required")
	}

	session, err := r.runtime.CreateWorkspace(ctx, req.Workspace)
	if err != nil {
		return RunResult{}, fmt.Errorf("create verification workspace: %w", err)
	}
	if session == nil || strings.TrimSpace(session.ID) == "" {
		return RunResult{}, errors.New("create verification workspace: runtime returned an empty session id")
	}

	result.SessionID = session.ID
	defer func() {
		if cleanupErr := r.runtime.DestroyWorkspace(ctx, session.ID); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("destroy verification workspace %s: %w", session.ID, cleanupErr))
		}
	}()

	result.Verification, err = r.verifier.Verify(ctx, Request{
		SessionID:     session.ID,
		WorkItem:      req.WorkItem,
		CandidateHead: req.CandidateHead,
		ChangedPaths:  req.ChangedPaths,
	})
	if err != nil {
		return result, err
	}
	return result, nil
}
