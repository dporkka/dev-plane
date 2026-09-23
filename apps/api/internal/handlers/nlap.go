package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/models"
)

// CreateNLAPTask ingests a canonical Nulang Agent Protocol goal/task pair and
// binds it to a Dev Plane repository. The protocol IDs and execution metadata
// are preserved while execution continues through the normal Dev Plane
// task/run/workspace pipeline.
func (h *Handler) CreateNLAPTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}
	if err := authz.AuthorizeProject(ctx, h.db, user, projectID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("project not found"))
		return
	}

	var req models.NLAPExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if err := req.Validate(); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	spec, metadata, acceptance, maxCost, maxRuntimeMinutes, err := req.DevPlaneFields()
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	sourceID := req.Task.ID
	description := req.Goal.Intent
	task, err := h.insertTask(ctx, createTaskOptions{
		ProjectID:          projectID,
		RepositoryID:       req.RepositoryID,
		CreatedBy:          user.UserID,
		Source:             "nlap",
		SourceID:           &sourceID,
		Title:              req.Task.Description,
		Description:        description,
		Priority:           "medium",
		RiskLevel:          "low",
		TargetBranch:       req.TargetBranch,
		MaxCost:            maxCost,
		MaxRuntimeMinutes:  maxRuntimeMinutes,
		Spec:               spec,
		AcceptanceCriteria: acceptance,
		Metadata:           metadata,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	h.logAgentVaultEvent(ctx, taskCreatedEvent(task, "nlap"))
	respond.JSON(w, http.StatusCreated, task)
}
