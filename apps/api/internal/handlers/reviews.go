// Package handlers provides HTTP handlers for the API service.
//
// Review handlers manage review reports for agent runs:
//   - GET /runs/{runId}/review    -> get review report
//   - POST /runs/{runId}/review   -> trigger manual review
package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/reviewer"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/vcs"
)

// GetReview returns the review report for an agent run.
func (h *Handler) GetReview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	runID := chi.URLParam(r, "runId")
	if runID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}

	if err := authz.AuthorizeAgentRun(ctx, h.db, user, runID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
		return
	}

	rev := reviewer.NewReviewer(h.db, h.logger)
	report, err := rev.Get(ctx, runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || err.Error() == "review report not found for run "+runID {
			respond.Error(w, http.StatusNotFound, errors.New("review report not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusOK, report)
}

// RequestReview triggers a manual review for a run.
func (h *Handler) RequestReview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	runID := chi.URLParam(r, "runId")
	if runID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}

	if err := authz.AuthorizeAgentRun(ctx, h.db, user, runID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
		return
	}

	// Verify the run exists
	var exists bool
	err := h.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_runs WHERE id = $1)`, runID).Scan(&exists)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if !exists {
		respond.Error(w, http.StatusNotFound, errors.New("agent run not found"))
		return
	}

	// A frozen reviewed candidate is immutable authority for this run. Repeated
	// review requests return the report already bound to that candidate rather
	// than overwriting the evidence with a new timestamp/digest.
	rev := reviewer.NewReviewer(h.db, h.logger)
	if _, frozenErr := vcs.LoadReviewedCandidate(ctx, h.db, runID); frozenErr == nil {
		report, getErr := rev.Get(ctx, runID)
		if getErr != nil {
			respond.Error(w, http.StatusInternalServerError, getErr)
			return
		}
		respond.JSON(w, http.StatusOK, report)
		return
	} else if !errors.Is(frozenErr, vcs.ErrReviewedCandidateNotFound) {
		respond.Error(w, http.StatusInternalServerError, frozenErr)
		return
	}

	// Capture and materialize one exact staged snapshot before review. This makes
	// untracked files part of the review surface and prevents workspace mutation
	// between review and candidate publication.
	snapshot, err := h.prepareReviewSnapshot(ctx, runID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	candidate, err := h.materializeReviewSnapshot(ctx, runID, snapshot)
	if err != nil {
		h.logger.Error("failed to materialize reviewed snapshot", "run_id", runID, "error", err)
		respond.Error(w, http.StatusConflict, err)
		return
	}
	report, err := rev.ReviewDiff(ctx, runID, snapshot.source.Diff, snapshot.securityWorkspacePath)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.saveReviewedCandidate(ctx, runID, snapshot, candidate, report); err != nil {
		h.logger.Error("failed to freeze reviewed candidate", "run_id", runID, "error", err)
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Publish review.completed event
	if h.eventBus != nil {
		event := map[string]interface{}{
			"run_id":     runID,
			"risk_level": report.RiskLevel,
			"approvable": report.Approvable,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}
		data, _ := json.Marshal(event)
		if pubErr := h.eventBus.Publish("review.completed", data); pubErr != nil {
			h.logger.Warn("failed to publish review.completed event", "error", pubErr)
		}
	}

	respond.JSON(w, http.StatusOK, report)
}

type reviewSnapshot struct {
	taskID                string
	workspaceID           string
	workspacePath         string
	securityWorkspacePath string
	source                vcs.ReviewSnapshot
	runner                vcs.CommandRunner
}

func (h *Handler) prepareReviewSnapshot(ctx context.Context, runID string) (reviewSnapshot, error) {
	var taskID string
	var workspaceID sql.NullString
	if err := h.db.QueryRowContext(ctx, `
		SELECT task_id, workspace_id
		FROM agent_runs
		WHERE id = $1
	`, runID).Scan(&taskID, &workspaceID); err != nil {
		return reviewSnapshot{}, err
	}
	if !workspaceID.Valid || workspaceID.String == "" {
		return reviewSnapshot{}, errors.New("reviewed agent run has no workspace")
	}

	workspace, err := h.loadWorkspaceRuntimeMetadata(ctx, workspaceID.String)
	if err != nil {
		return reviewSnapshot{}, err
	}

	snapshot := reviewSnapshot{
		taskID:        taskID,
		workspaceID:   workspaceID.String,
		workspacePath: ".",
		runner:        vcs.ExecRunner{},
	}
	if workspace.WorktreePath != nil && *workspace.WorktreePath != "" {
		snapshot.workspacePath = *workspace.WorktreePath
		snapshot.securityWorkspacePath = *workspace.WorktreePath
	} else {
		workspace, provider, runtimeErr := h.getRuntimeWorkspace(ctx, workspaceID.String)
		if runtimeErr != nil {
			return reviewSnapshot{}, runtimeErr
		}
		if provider == nil || workspace.RuntimeSessionID == nil || *workspace.RuntimeSessionID == "" {
			return reviewSnapshot{}, errors.New("reviewed workspace has neither a local worktree nor an attached runtime")
		}
		snapshot.runner = runtimes.NewVCSCommandRunner(provider, *workspace.RuntimeSessionID)
	}

	source, err := vcs.CaptureReviewSnapshot(ctx, snapshot.runner, snapshot.workspacePath)
	if err != nil {
		return reviewSnapshot{}, err
	}
	snapshot.source = source
	return snapshot, nil
}

func (h *Handler) materializeReviewSnapshot(ctx context.Context, runID string, snapshot reviewSnapshot) (vcs.VerifiedCandidate, error) {
	return vcs.MaterializeReviewSnapshot(
		ctx,
		snapshot.runner,
		snapshot.workspacePath,
		"reviewed candidate for task "+snapshot.taskID+" run "+runID,
		vcs.CandidateMetadata{
			TaskID:      snapshot.taskID,
			AgentID:     runID,
			WorkspaceID: snapshot.workspaceID,
		},
		snapshot.source,
	)
}

func (h *Handler) saveReviewedCandidate(
	ctx context.Context,
	runID string,
	snapshot reviewSnapshot,
	candidate vcs.VerifiedCandidate,
	report *reviewer.ReviewReport,
) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	return vcs.SaveReviewedCandidate(ctx, h.db, vcs.ReviewedCandidate{
		RunID:        runID,
		TaskID:       snapshot.taskID,
		WorkspaceID:  snapshot.workspaceID,
		ReviewDigest: hex.EncodeToString(sum[:]),
		Candidate:    candidate,
	})
}
