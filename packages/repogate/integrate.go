package repogate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ai-dev-control-plane/vcs"
)

func (g *Gate) IntegrateApproved(ctx context.Context, runID, taskID string) (Record, error) {
	record, err := g.loadRecord(ctx, runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, ErrCandidateNotReady
		}
		return Record{}, err
	}
	if taskID != "" && record.TaskID != taskID {
		return Record{}, fmt.Errorf("candidate run %s belongs to task %s, not %s", runID, record.TaskID, taskID)
	}
	if record.Status == StatusIntegrated {
		return record, nil
	}
	if record.Status != StatusVerified {
		return Record{}, fmt.Errorf("%w: status %s", ErrCandidateNotReady, record.Status)
	}
	scope, err := g.loadWorkspaceContext(ctx, runID)
	if err != nil {
		return Record{}, err
	}
	runner, repoPath, err := g.commandRunner(ctx, scope)
	if err != nil {
		return Record{}, err
	}
	verifier, err := newVerifier(runner, validationCommands(scope))
	if err != nil {
		return Record{}, err
	}
	target := strings.TrimSpace(record.TargetBranch)
	if target == "" {
		target = strings.TrimSpace(scope.baseBranch)
	}
	if target == "" {
		target = "main"
	}
	branch := integrationBranch(record.TaskID)
	if _, err := runner.Run(ctx, vcs.Command{
		Name: "git",
		Args: []string{"branch", "-f", branch, target},
		Dir: repoPath,
	}); err != nil {
		return Record{}, fmt.Errorf("prepare integration branch %s from %s: %w", branch, target, err)
	}
	refinery, err := vcs.NewMergeRefinery(filepath.Join(".dev-plane", "refinery"), runner, g.recorder)
	if err != nil {
		return Record{}, err
	}
	outcome, err := refinery.Refine(ctx, vcs.MergeRequest{
		RepositoryPath: repoPath,
		TargetRef: branch,
		Candidate: vcs.VerifiedCandidate{
			Revision: vcs.Revision{CommitID: record.CommitID, ChangeID: record.ChangeID},
			Branch: record.SourceBranch,
			Metadata: vcs.CandidateMetadata{
				TaskID: record.TaskID,
				AgentID: scope.agentRole,
				WorkspaceID: record.WorkspaceID,
			},
		},
		Verifier: verifier,
	})
	if err != nil {
		return Record{}, fmt.Errorf("refine approved candidate: %w", err)
	}
	record.IntegrationBranch = branch
	record.FinalEvidence = outcome.Verification.Evidence
	switch outcome.Status {
	case vcs.MergeStatusMerged, vcs.MergeStatusAlreadyIntegrated:
		record.Status = StatusIntegrated
		record.IntegratedCommit = outcome.NewHead
		if record.IntegratedCommit == "" {
			record.IntegratedCommit = outcome.PreviousHead
		}
	case vcs.MergeStatusConflict, vcs.MergeStatusStaleTarget:
		record.Status = StatusConflict
	default:
		record.Status = StatusRejected
	}
	if err := g.saveRecord(ctx, record); err != nil {
		return Record{}, err
	}
	if record.Status != StatusIntegrated {
		return record, fmt.Errorf("candidate integration blocked: %s", outcome.Status)
	}
	if _, err := g.db.ExecContext(ctx, `
		UPDATE workspaces
		SET branch = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND deleted_at IS NULL
	`, record.IntegrationBranch, record.WorkspaceID); err != nil {
		return Record{}, fmt.Errorf("record integrated workspace branch: %w", err)
	}
	return record, nil
}
