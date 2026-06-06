// Package handlers provides HTTP handlers for the API service.
//
// Review handlers manage review reports for agent runs:
//   - GET /runs/{runId}/review    -> get review report
//   - POST /runs/{runId}/review   -> trigger manual review
package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/reviewer"
)

// GetReview returns the review report for an agent run.
func (h *Handler) GetReview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runID := chi.URLParam(r, "runId")
	if runID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
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
	runID := chi.URLParam(r, "runId")
	if runID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("run id is required"))
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

	// Trigger the review
	rev := reviewer.NewReviewer(h.db, h.logger)
	report, err := rev.Review(ctx, runID)
	if err != nil {
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
