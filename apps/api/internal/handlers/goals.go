package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
	models "github.com/ai-dev-control-plane/models"
)

// GoalStore is the narrow persistence contract needed by the HTTP Goal surface.
// Derived evaluation remains in the models package and does not mutate storage.
type GoalStore interface {
	PutGoal(context.Context, models.Goal) (models.Goal, error)
	GetGoal(context.Context, string) (models.Goal, error)
	LinkGoalWorkItem(context.Context, string, string, string) (models.Goal, error)
	ListGoalProofs(context.Context, string) ([]models.GoalProof, error)
}

type CreateGoalRequest struct {
	Title           string                 `json:"title"`
	Objective       string                 `json:"objective"`
	SuccessCriteria []models.GoalCriterion `json:"success_criteria"`
	MaxCost         *float64               `json:"max_cost,omitempty"`
	Deadline        *time.Time             `json:"deadline,omitempty"`
	Metadata        json.RawMessage        `json:"metadata,omitempty"`
}

type LinkGoalWorkItemRequest struct {
	WorkItemID      string `json:"work_item_id"`
	SubjectRevision string `json:"subject_revision"`
}

type GoalEvaluationResponse struct {
	Goal       models.Goal           `json:"goal"`
	Evaluation models.GoalEvaluation `json:"evaluation"`
}

func (h *Handler) CreateGoal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	if h.goalStore == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("goal store is unavailable"))
		return
	}

	orgID := strings.TrimSpace(chi.URLParam(r, "orgID"))
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}
	if err := authz.AuthorizeOrganization(ctx, h.db, user, orgID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
		return
	}

	var req CreateGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	goal := models.Goal{
		ID:              uuid.NewString(),
		OrganizationID:  orgID,
		CreatedBy:       user.UserID,
		Title:           strings.TrimSpace(req.Title),
		Objective:       strings.TrimSpace(req.Objective),
		Status:          models.GoalStatusActive,
		SuccessCriteria: req.SuccessCriteria,
		MaxCost:         req.MaxCost,
		Deadline:        req.Deadline,
		Metadata:        req.Metadata,
	}
	stored, err := h.goalStore.PutGoal(ctx, goal)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	respond.JSON(w, http.StatusCreated, stored)
}

func (h *Handler) GetGoal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	goal, ok := h.authorizedGoal(w, ctx, user.OrgID, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	respond.JSON(w, http.StatusOK, goal)
}

func (h *Handler) EvaluateGoal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	goal, ok := h.authorizedGoal(w, ctx, user.OrgID, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	proofs, err := h.goalStore.ListGoalProofs(ctx, goal.ID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	respond.JSON(w, http.StatusOK, GoalEvaluationResponse{
		Goal:       goal,
		Evaluation: models.EvaluateGoalProof(goal, proofs),
	})
}

func (h *Handler) LinkGoalWorkItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}
	goal, ok := h.authorizedGoal(w, ctx, user.OrgID, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	var req LinkGoalWorkItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	req.WorkItemID = strings.TrimSpace(req.WorkItemID)
	req.SubjectRevision = strings.TrimSpace(req.SubjectRevision)
	if req.WorkItemID == "" || req.SubjectRevision == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("work_item_id and subject_revision are required"))
		return
	}

	stored, err := h.goalStore.LinkGoalWorkItem(ctx, goal.ID, req.WorkItemID, req.SubjectRevision)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	respond.JSON(w, http.StatusOK, stored)
}

func (h *Handler) authorizedGoal(w http.ResponseWriter, ctx context.Context, orgID, goalID string) (models.Goal, bool) {
	if h.goalStore == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("goal store is unavailable"))
		return models.Goal{}, false
	}
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("goal id is required"))
		return models.Goal{}, false
	}
	goal, err := h.goalStore.GetGoal(ctx, goalID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("goal not found"))
			return models.Goal{}, false
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return models.Goal{}, false
	}
	if goal.OrganizationID != orgID {
		respond.Error(w, http.StatusNotFound, errors.New("goal not found"))
		return models.Goal{}, false
	}
	return goal, true
}
