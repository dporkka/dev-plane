package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// CreateBriefHandoffRequest creates a Dev Plane task from a Dev Plan Builder's Brief.
type CreateBriefHandoffRequest struct {
	RepositoryID       string            `json:"repository_id"`
	BriefProjectID     string            `json:"brief_project_id,omitempty"`
	BriefURL           string            `json:"brief_url,omitempty"`
	BriefZipURL        string            `json:"brief_zip_url,omitempty"`
	Title              string            `json:"title,omitempty"`
	Description        string            `json:"description,omitempty"`
	Priority           string            `json:"priority,omitempty"`
	RiskLevel          string            `json:"risk_level,omitempty"`
	TargetBranch       string            `json:"target_branch,omitempty"`
	AcceptanceCriteria []string          `json:"acceptance_criteria,omitempty"`
	Documents          []BriefDocument   `json:"documents,omitempty"`
	Constraints        map[string]string `json:"constraints,omitempty"`
}

// BriefDocument is a document pointer from a Builder's Brief.
type BriefDocument struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	Content string `json:"content,omitempty"`
}

// CreateBriefHandoff creates a task from a Dev Plan Builder's Brief handoff.
func (h *Handler) CreateBriefHandoff(w http.ResponseWriter, r *http.Request) {
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

	var req CreateBriefHandoffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.RepositoryID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("repository_id is required"))
		return
	}
	if req.BriefURL == "" && req.BriefZipURL == "" && len(req.Documents) == 0 {
		respond.Error(w, http.StatusBadRequest, errors.New("brief_url, brief_zip_url, or documents is required"))
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Build from Builder's Brief"
	}

	spec, err := json.Marshal(map[string]any{
		"kind":                "dev_plan_brief",
		"brief_project_id":    req.BriefProjectID,
		"brief_url":           req.BriefURL,
		"brief_zip_url":       req.BriefZipURL,
		"acceptance_criteria": req.AcceptanceCriteria,
		"documents":           req.Documents,
		"constraints":         req.Constraints,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	metadata, err := json.Marshal(map[string]any{
		"handoff_source":   "dev-plan",
		"brief_project_id": req.BriefProjectID,
		"brief_url":        req.BriefURL,
		"brief_zip_url":    req.BriefZipURL,
		"document_count":   len(req.Documents),
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	var sourceID *string
	if req.BriefProjectID != "" {
		sourceID = &req.BriefProjectID
	}

	task, err := h.insertTask(ctx, createTaskOptions{
		ProjectID:    projectID,
		RepositoryID: req.RepositoryID,
		CreatedBy:    user.UserID,
		Source:       "dev_plan_brief",
		SourceID:     sourceID,
		Title:        title,
		Description:  req.Description,
		Priority:     req.Priority,
		RiskLevel:    req.RiskLevel,
		TargetBranch: req.TargetBranch,
		Spec:         spec,
		Metadata:     metadata,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	h.logAgentVaultEvent(ctx, taskCreatedEvent(task, "dev-plan"))
	respond.JSON(w, http.StatusCreated, task)
}
