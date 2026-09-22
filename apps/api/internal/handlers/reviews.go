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

	// Trigger the review only when no immutable candidate has been frozen yet.
	report, err := rev.Review(ctx, runID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.freezeReviewedCandidate(ctx, runID, report); err != nil {
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

func (h *Handler) freezeReviewedCandidate(ctx context.Context, runID string, report *reviewer.ReviewReport) error {
	var taskID string
	var workspaceID sql.NullString
	if err := h.db.QueryRowContext(ctx, `
		SELECT task_id, workspace_id
		FROM agent_runs
		WHERE id = $1
	`, runID).Scan(&taskID, &workspaceID); err != nil {
		return err
	}
	if !workspaceID.Valid || workspaceID.String == "" {
		return errors.New("reviewed agent run has no workspace")
	}

	workspace, err := h.loadWorkspaceRuntimeMetadata(ctx, workspaceID.String)
	if err != nil {
		return err
	}
	workspacePath := "."
	var runner vcs.CommandRunner
	if workspace.WorktreePath != nil && *workspace.WorktreePath != "" {
		workspacePath = *workspace.WorktreePath
	} else {
		workspace, provider, err := h.getRuntimeWorkspace(ctx, workspaceID.String)
		if err != nil {
			return err
		}
		if provider == nil || workspace.RuntimeSessionID == nil || *workspace.RuntimeSessionID == "" {
			return errors.New("reviewed workspace has neither a local worktree nor an attached runtime")
		}
		runner = runtimes.NewVCSCommandRunner(provider, *workspace.RuntimeSessionID)
	}

	candidate, err := vcs.NewCandidateMaterializer(runner, nil).Materialize(
		ctx,
		workspacePath,
		"reviewed candidate for task "+taskID+" run "+runID,
		vcs.CandidateMetadata{
			TaskID:      taskID,
			AgentID:     runID,
			WorkspaceID: workspaceID.String,
		},
	)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	return vcs.SaveReviewedCandidate(ctx, h.db, vcs.ReviewedCandidate{
		RunID:        runID,
		TaskID:       taskID,
		WorkspaceID:  workspaceID.String,
		ReviewDigest: hex.EncodeToString(sum[:]),
		Candidate:    candidate,
	})
}
