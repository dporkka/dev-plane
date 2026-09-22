package repogate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ai-dev-control-plane/vcs"
)

func (g *Gate) PrepareReviewedCandidate(ctx context.Context, runID, taskID string) (Record, error) {
	if g.db == nil {
		return Record{}, fmt.Errorf("repository gate database is required")
	}
	if existing, err := g.loadRecord(ctx, runID); err == nil {
		if taskID != "" && existing.TaskID != taskID {
			return Record{}, fmt.Errorf("candidate run %s belongs to task %s, not %s", runID, existing.TaskID, taskID)
		}
		if existing.Status == StatusVerified || existing.Status == StatusIntegrated {
			return existing, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Record{}, err
	}
	scope, err := g.loadWorkspaceContext(ctx, runID)
	if err != nil {
		return Record{}, err
	}
	if taskID != "" && scope.taskID != taskID {
		return Record{}, fmt.Errorf("run %s belongs to task %s, event referenced task %s", runID, scope.taskID, taskID)
	}
	if !scope.approvable {
		return Record{}, ErrReviewNotApprovable
	}
	runner, repoPath, err := g.commandRunner(ctx, scope)
	if err != nil {
		return Record{}, err
	}
	materializer := vcs.NewCandidateMaterializer(runner, g.recorder)
	candidate, err := materializer.Materialize(ctx, repoPath,
		fmt.Sprintf("dev-plane: verified candidate for task %s run %s", scope.taskID, runID),
		vcs.CandidateMetadata{TaskID: scope.taskID, AgentID: scope.agentRole, WorkspaceID: scope.workspaceID})
	if err != nil {
		return Record{}, fmt.Errorf("materialize reviewed candidate: %w", err)
	}
	verifier, err := newVerifier(runner, validationCommands(scope))
	if err != nil {
		return Record{}, err
	}
	report, err := verifier.Verify(ctx, repoPath)
	if err != nil {
		return Record{}, fmt.Errorf("verify materialized candidate: %w", err)
	}
	record := Record{
		RunID: runID, TaskID: scope.taskID, WorkspaceID: scope.workspaceID,
		CommitID: candidate.Revision.CommitID, ChangeID: candidate.Revision.ChangeID,
		SourceBranch: candidate.Branch, TargetBranch: scope.targetBranch,
		Status: StatusVerified, InitialEvidence: report.Evidence,
	}
	if !report.Passed {
		record.Status = StatusRejected
		if err := g.saveRecord(ctx, record); err != nil {
			return Record{}, err
		}
		return record, ErrCandidateRejected
	}
	if err := g.saveRecord(ctx, record); err != nil {
		return Record{}, err
	}
	return record, nil
}
