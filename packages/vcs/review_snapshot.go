package vcs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// ReviewSnapshot is the exact staged patch presented to the reviewer. BaseCommit
// anchors the patch so the later immutable candidate can be verified against the
// same source bytes before it becomes publication authority.
type ReviewSnapshot struct {
	BaseCommit string `json:"base_commit"`
	Diff       string `json:"diff"`
}

// CaptureReviewSnapshot stages all workspace changes, including untracked files,
// then captures the exact binary-capable patch against HEAD.
func CaptureReviewSnapshot(ctx context.Context, runner CommandRunner, workspacePath string) (ReviewSnapshot, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	if strings.TrimSpace(workspacePath) == "" {
		return ReviewSnapshot{}, fmt.Errorf("workspace path is required")
	}

	head, err := runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"rev-parse", "--verify", "HEAD^{commit}"},
		Dir:  workspacePath,
	})
	if err != nil {
		return ReviewSnapshot{}, fmt.Errorf("resolve review base: %w", err)
	}
	base := strings.TrimSpace(head.Stdout)
	if base == "" {
		return ReviewSnapshot{}, errors.New("review workspace has no HEAD commit")
	}

	if _, err := runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"add", "-A", "--"},
		Dir:  workspacePath,
	}); err != nil {
		return ReviewSnapshot{}, fmt.Errorf("stage review snapshot: %w", err)
	}
	patch, err := runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"diff", "--cached", "--no-ext-diff", "--binary", "--full-index", "HEAD", "--"},
		Dir:  workspacePath,
	})
	if err != nil {
		return ReviewSnapshot{}, fmt.Errorf("capture review patch: %w", err)
	}
	return ReviewSnapshot{BaseCommit: base, Diff: patch.Stdout}, nil
}

// MaterializeReviewSnapshot commits the staged workspace and proves that the
// resulting immutable candidate is byte-for-byte the patch that was reviewed.
// If the workspace changed after capture, the generated commit is rolled back
// while preserving working-tree changes so the caller can review again.
func MaterializeReviewSnapshot(
	ctx context.Context,
	runner CommandRunner,
	workspacePath, message string,
	metadata CandidateMetadata,
	snapshot ReviewSnapshot,
) (VerifiedCandidate, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	if strings.TrimSpace(snapshot.BaseCommit) == "" {
		return VerifiedCandidate{}, fmt.Errorf("review snapshot base commit is required")
	}

	candidate, err := NewCandidateMaterializer(runner, nil).Materialize(
		ctx, workspacePath, message, metadata,
	)
	if err != nil {
		return VerifiedCandidate{}, err
	}

	materialized, err := runner.Run(ctx, Command{
		Name: "git",
		Args: []string{
			"diff", "--no-ext-diff", "--binary", "--full-index",
			snapshot.BaseCommit, candidate.Revision.CommitID, "--",
		},
		Dir: workspacePath,
	})
	if err != nil {
		return VerifiedCandidate{}, fmt.Errorf("verify materialized review patch: %w", err)
	}
	if sha256.Sum256([]byte(materialized.Stdout)) != sha256.Sum256([]byte(snapshot.Diff)) {
		if candidate.Revision.CommitID != snapshot.BaseCommit {
			_, _ = runner.Run(context.Background(), Command{
				Name: "git",
				Args: []string{"reset", "--mixed", snapshot.BaseCommit},
				Dir:  workspacePath,
			})
		}
		return VerifiedCandidate{}, errors.New("workspace changed while freezing review snapshot; candidate rejected")
	}
	return candidate, nil
}
