package vcs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// CandidateMetadata attributes an immutable verified candidate to the agent work
// that produced it. These fields are deliberately independent of Git commit
// author metadata so provenance survives rebases and integration rewrites.
type CandidateMetadata struct {
	TaskID      string `json:"task_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// VerifiedCandidate is the immutable output of a successful verification stage.
// CommitID is the artifact integrated by MergeRefinery; ChangeID remains stable
// for VCS backends that support evolution-stable change identities.
type VerifiedCandidate struct {
	Revision Revision          `json:"revision"`
	Branch   string            `json:"branch"`
	Changed  bool              `json:"changed"`
	Metadata CandidateMetadata `json:"metadata,omitempty"`
}

// CandidateMaterializer converts an already-verified Git workspace into an
// immutable candidate commit. Repository-local hooks and commit signing are
// outside the autonomous trust boundary and are disabled during materialization.
type CandidateMaterializer struct {
	runner   CommandRunner
	recorder Recorder
}

func NewCandidateMaterializer(runner CommandRunner, recorder Recorder) *CandidateMaterializer {
	if runner == nil {
		runner = ExecRunner{}
	}
	if recorder == nil {
		recorder = NopRecorder{}
	}
	return &CandidateMaterializer{runner: runner, recorder: recorder}
}

// Materialize captures workspace state after verification. A clean workspace is
// a valid no-op candidate and reuses HEAD rather than manufacturing an empty
// commit.
func (m *CandidateMaterializer) Materialize(
	ctx context.Context,
	workspacePath, message string,
	metadata CandidateMetadata,
) (VerifiedCandidate, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return VerifiedCandidate{}, fmt.Errorf("workspace path is required")
	}
	if strings.TrimSpace(message) == "" {
		return VerifiedCandidate{}, fmt.Errorf("candidate message is required")
	}

	branchResult, err := m.runner.Run(ctx, Command{
		Name: "git", Args: []string{"branch", "--show-current"}, Dir: workspacePath,
	})
	if err != nil {
		return VerifiedCandidate{}, fmt.Errorf("resolve candidate branch: %w", err)
	}
	branch := strings.TrimSpace(branchResult.Stdout)
	if branch == "" {
		return VerifiedCandidate{}, fmt.Errorf("verified workspace is detached; refusing to materialize candidate")
	}

	status, err := m.runner.Run(ctx, Command{
		Name: "git",
		Args: []string{"status", "--porcelain=v1", "--untracked-files=all"},
		Dir:  workspacePath,
	})
	if err != nil {
		return VerifiedCandidate{}, fmt.Errorf("inspect verified workspace: %w", err)
	}

	changed := strings.TrimSpace(status.Stdout) != ""
	if changed {
		if _, err := m.runner.Run(ctx, Command{
			Name: "git", Args: []string{"add", "-A", "--"}, Dir: workspacePath,
		}); err != nil {
			return VerifiedCandidate{}, fmt.Errorf("stage verified workspace: %w", err)
		}

		staged, err := m.runner.Run(ctx, Command{
			Name: "git", Args: []string{"diff", "--cached", "--name-only"}, Dir: workspacePath,
		})
		if err != nil {
			return VerifiedCandidate{}, fmt.Errorf("inspect staged candidate: %w", err)
		}
		changed = strings.TrimSpace(staged.Stdout) != ""
		if changed {
			if _, err := m.runner.Run(ctx, Command{
				Name: "git",
				Args: []string{
					"-c", "user.name=Dev Plane Agent",
					"-c", "user.email=agent@dev-plane.invalid",
					"-c", "commit.gpgsign=false",
					"-c", "core.hooksPath=/dev/null",
					"commit", "--no-verify", "--no-gpg-sign", "-m", message,
				},
				Dir: workspacePath,
			}); err != nil {
				return VerifiedCandidate{}, fmt.Errorf("commit verified candidate: %w", err)
			}
		}
	}

	revision, err := NewGitBackend(m.runner).revision(ctx, workspacePath)
	if err != nil {
		return VerifiedCandidate{}, fmt.Errorf("resolve candidate revision: %w", err)
	}
	candidate := VerifiedCandidate{
		Revision: revision,
		Branch:   branch,
		Changed:  changed,
		Metadata: metadata,
	}
	if err := m.recorder.Record(ctx, ProvenanceEvent{
		Kind:      EventKind("candidate.materialized"),
		Backend:   "git",
		TaskID:    metadata.TaskID,
		AgentID:   metadata.AgentID,
		Workspace: workspacePath,
		CommitID:  revision.CommitID,
		ChangeID:  revision.ChangeID,
		Attributes: map[string]string{
			"workspace_id": metadata.WorkspaceID,
			"branch":       branch,
			"changed":      strconv.FormatBool(changed),
		},
	}); err != nil {
		return VerifiedCandidate{}, fmt.Errorf("record candidate provenance: %w", err)
	}
	return candidate, nil
}
