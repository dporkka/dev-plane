// Package handlers provides HTTP handlers for the API service.
//
// Pull request handlers manage PRs created by the agent system:
//   - GET /projects/{projectID}/pull-requests  -> list PRs for a project
//   - GET /pull-requests/{id}                   -> get PR by ID
//   - POST /tasks/{taskId}/pull-request         -> create PR for a task
package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/prfactory"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// PullRequestResponse is the API representation of a pull request.
type PullRequestResponse struct {
	ID         string     `json:"id"`
	TaskID     string     `json:"task_id"`
	RunID      *string    `json:"run_id,omitempty"`
	RepoID     string     `json:"repository_id"`
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Branch     string     `json:"branch"`
	BaseBranch string     `json:"base_branch"`
	URL        string     `json:"url"`
	State      string     `json:"state"`
	Draft      bool       `json:"draft"`
	CreatedBy  string     `json:"created_by"`
	MergedAt   *time.Time `json:"merged_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ListPullRequests returns PRs for a project.
func (h *Handler) ListPullRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}

	// Get repository IDs for the project
	rows, err := h.db.QueryContext(ctx, `
		SELECT id FROM repositories WHERE project_id = $1
	`, projectID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var repoIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		repoIDs = append(repoIDs, id)
	}

	if len(repoIDs) == 0 {
		respond.JSON(w, http.StatusOK, []PullRequestResponse{})
		return
	}

	// Build parameterized query for IN clause
	query := `
		SELECT id, task_id, run_id, repository_id, number, title, body,
		       branch, base_branch, url, state, draft, created_by, merged_at, created_at, updated_at
		FROM pull_requests
		WHERE repository_id IN (`
	args := make([]interface{}, len(repoIDs))
	for i, id := range repoIDs {
		if i > 0 {
			query += ", "
		}
		query += fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	query += `) ORDER BY created_at DESC`

	prRows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer prRows.Close()

	var prs []PullRequestResponse
	for prRows.Next() {
		var pr PullRequestResponse
		var runID sql.NullString
		var mergedAt sql.NullTime
		err := prRows.Scan(
			&pr.ID, &pr.TaskID, &runID, &pr.RepoID, &pr.Number, &pr.Title, &pr.Body,
			&pr.Branch, &pr.BaseBranch, &pr.URL, &pr.State, &pr.Draft, &pr.CreatedBy,
			&mergedAt, &pr.CreatedAt, &pr.UpdatedAt,
		)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if runID.Valid {
			pr.RunID = &runID.String
		}
		if mergedAt.Valid {
			pr.MergedAt = &mergedAt.Time
		}
		prs = append(prs, pr)
	}

	if prs == nil {
		prs = []PullRequestResponse{}
	}
	respond.JSON(w, http.StatusOK, prs)
}

// GetPullRequest returns a PR by ID.
func (h *Handler) GetPullRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("pull request id is required"))
		return
	}

	var pr PullRequestResponse
	var runID sql.NullString
	var mergedAt sql.NullTime

	err := h.db.QueryRowContext(ctx, `
		SELECT id, task_id, run_id, repository_id, number, title, body,
		       branch, base_branch, url, state, draft, created_by, merged_at, created_at, updated_at
		FROM pull_requests WHERE id = $1
	`, id).Scan(
		&pr.ID, &pr.TaskID, &runID, &pr.RepoID, &pr.Number, &pr.Title, &pr.Body,
		&pr.Branch, &pr.BaseBranch, &pr.URL, &pr.State, &pr.Draft, &pr.CreatedBy,
		&mergedAt, &pr.CreatedAt, &pr.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("pull request not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if runID.Valid {
		pr.RunID = &runID.String
	}
	if mergedAt.Valid {
		pr.MergedAt = &mergedAt.Time
	}

	respond.JSON(w, http.StatusOK, pr)
}

// CreatePullRequestRequest is the request body for creating a PR.
type CreatePullRequestRequest struct {
	Approved bool `json:"approved,omitempty"`
}

// CreatePullRequest creates a PR for a task after human approval.
func (h *Handler) CreatePullRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	taskID := chi.URLParam(r, "taskId")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	// Parse optional request body
	var req CreatePullRequestRequest
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// Verify task exists and is in a valid state for PR creation
	var task struct {
		Status   string
		RepoID   string
		Branch   string
	}
	err := h.db.QueryRowContext(ctx, `
		SELECT status, repository_id, target_branch
		FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&task.Status, &task.RepoID, &task.Branch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("task not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Allow PR creation from reviewing or pr_created states
	if task.Status != "reviewing" && task.Status != "pr_created" {
		respond.Error(w, http.StatusBadRequest,
			fmt.Errorf("task must be in 'reviewing' or 'pr_created' status, current: %s", task.Status))
		return
	}

	// Check for pending approvals if not explicitly approved via request
	if !req.Approved {
		var pendingCount int
		err := h.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM approvals
			WHERE task_id = $1 AND response IS NULL
			AND (expires_at IS NULL OR expires_at > $2)
		`, taskID, time.Now().UTC()).Scan(&pendingCount)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if pendingCount > 0 {
			respond.Error(w, http.StatusConflict, errors.New("pending approval exists for this task"))
			return
		}
	}

	// Create the pull request using the factory
	factory := prfactory.NewFactory(h.db, h.logger)
	pr, err := factory.CreatePullRequest(ctx, taskID)
	if err != nil {
		h.logger.Error("failed to create pull request", "task_id", taskID, "error", err)
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("create pull request: %w", err))
		return
	}

	// Publish pr.created event
	if h.eventBus != nil {
		event := map[string]interface{}{
			"pr_id":      pr.ID,
			"task_id":    taskID,
			"pr_number":  pr.Number,
			"branch":     pr.Branch,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}
		data, _ := json.Marshal(event)
		if pubErr := h.eventBus.Publish("pr.created", data); pubErr != nil {
			h.logger.Warn("failed to publish pr.created event", "error", pubErr)
		}
	}

	respond.JSON(w, http.StatusCreated, PullRequestResponse{
		ID:         pr.ID,
		TaskID:     pr.TaskID,
		RunID:      pr.RunID,
		RepoID:     pr.RepoID,
		Number:     pr.Number,
		Title:      pr.Title,
		Body:       pr.Body,
		Branch:     pr.Branch,
		BaseBranch: pr.BaseBranch,
		URL:        pr.URL,
		State:      pr.State,
		Draft:      pr.Draft,
		CreatedBy:  pr.CreatedBy,
		MergedAt:   pr.MergedAt,
		CreatedAt:  pr.CreatedAt,
		UpdatedAt:  pr.UpdatedAt,
	})
}
