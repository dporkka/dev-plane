package agentrunner

import (
	"context"
	"fmt"
	"os"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/scheduler"
)

// CompletionVerification is the authority-bearing result of the canonical final
// verification path. It does not imply that the AgentRun status was changed.
type CompletionVerification struct {
	SubjectRevision string
	Evidence        []scheduler.Evidence
	Results         map[string]any
}

// VerifyRunCompletion runs the same revision-bound final verification used by
// the legacy runner without mutating the run lifecycle status. This is the
// reusable completion authority for external execution backends.
func (r *Runner) VerifyRunCompletion(ctx context.Context, runID string) (CompletionVerification, error) {
	if r == nil {
		return CompletionVerification{}, fmt.Errorf("agent runner is not configured")
	}
	if r.completionObserver == nil {
		return CompletionVerification{}, fmt.Errorf("completion evidence observer is required")
	}

	run, err := r.loadAgentRun(ctx, runID)
	if err != nil {
		return CompletionVerification{}, fmt.Errorf("load agent run %s: %w", runID, err)
	}
	task, err := r.loadTask(ctx, run.TaskID)
	if err != nil {
		return CompletionVerification{}, fmt.Errorf("load task: %w", err)
	}
	workspace, err := r.loadWorkspace(ctx, run.WorkspaceID)
	if err != nil {
		return CompletionVerification{}, fmt.Errorf("load workspace: %w", err)
	}
	workspacePath := r.getWorkspacePath(workspace)
	runtimeProvider, _, runtimeErr := r.runtimeProviderForWorkspace(ctx, workspace)
	if runtimeErr != nil {
		return CompletionVerification{}, fmt.Errorf("workspace runtime not accessible: %w", runtimeErr)
	}
	if runtimeProvider == nil {
		if workspacePath == "" {
			return CompletionVerification{}, fmt.Errorf("workspace path is empty")
		}
		if _, err := os.Stat(workspacePath); err != nil {
			return CompletionVerification{}, fmt.Errorf("workspace path not accessible: %w", err)
		}
	}

	return r.verifyLoadedRunCompletion(ctx, run, task, workspace, workspacePath, true)
}

func (r *Runner) verifyLoadedRunCompletion(
	ctx context.Context,
	run *models.AgentRun,
	task *models.Task,
	workspace *models.Workspace,
	workspacePath string,
	requireObserver bool,
) (CompletionVerification, error) {
	if requireObserver && r.completionObserver == nil {
		return CompletionVerification{}, fmt.Errorf("completion evidence observer is required")
	}

	finalChecks, err := r.runFinalChecks(ctx, run, task, workspace, workspacePath)
	if err != nil {
		return CompletionVerification{}, fmt.Errorf("final verification: %w", err)
	}
	if !finalChecks.Passed {
		return CompletionVerification{}, fmt.Errorf("final verification failed")
	}

	if r.completionObserver != nil {
		if err := r.completionObserver.RecordRunCompletion(
			ctx,
			run.ID,
			finalChecks.SubjectRevision,
			finalChecks.Evidence,
		); err != nil {
			return CompletionVerification{}, fmt.Errorf("completion evidence rejected: %w", err)
		}
	}

	return CompletionVerification{
		SubjectRevision: finalChecks.SubjectRevision,
		Evidence:        append([]scheduler.Evidence(nil), finalChecks.Evidence...),
		Results:         finalChecks.Results,
	}, nil
}
