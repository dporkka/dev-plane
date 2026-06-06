package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/events"
)

// Task represents a task record.
type Task struct {
	ID                   string          `json:"id"`
	ProjectID            string          `json:"project_id"`
	RepositoryID         string          `json:"repository_id"`
	WorkspaceID          *string         `json:"workspace_id,omitempty"`
	CreatedBy            string          `json:"created_by"`
	Source               string          `json:"source"`
	SourceID             *string         `json:"source_id,omitempty"`
	Title                string          `json:"title"`
	Description          *string         `json:"description,omitempty"`
	Status               string          `json:"status"`
	Priority             string          `json:"priority"`
	RiskLevel            string          `json:"risk_level"`
	TargetBranch         string          `json:"target_branch"`
	Spec                 json.RawMessage `json:"spec,omitempty"`
	AcceptanceCriteria   json.RawMessage `json:"acceptance_criteria,omitempty"`
	MaxCost              *float64        `json:"max_cost,omitempty"`
	MaxRuntimeMinutes    int             `json:"max_runtime_minutes"`
	ApprovalRequirements json.RawMessage `json:"approval_requirements,omitempty"`
	Metadata             json.RawMessage `json:"metadata,omitempty"`
	StartedAt            *time.Time      `json:"started_at,omitempty"`
	CompletedAt          *time.Time      `json:"completed_at,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// CreateTaskRequest is the request body for creating a task.
type CreateTaskRequest struct {
	RepositoryID string          `json:"repository_id"`
	Title        string          `json:"title"`
	Description  string          `json:"description,omitempty"`
	Priority     string          `json:"priority,omitempty"`
	RiskLevel    string          `json:"risk_level,omitempty"`
	TargetBranch string          `json:"target_branch,omitempty"`
	MaxCost      *float64        `json:"max_cost,omitempty"`
	Spec         json.RawMessage `json:"spec,omitempty"`
}

type createTaskOptions struct {
	ProjectID    string
	RepositoryID string
	CreatedBy    string
	Source       string
	SourceID     *string
	Title        string
	Description  string
	Priority     string
	RiskLevel    string
	TargetBranch string
	MaxCost      *float64
	Spec         json.RawMessage
	Metadata     json.RawMessage
}

// UpdateTaskRequest is the request body for updating a task.
type UpdateTaskRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Priority    *string `json:"priority,omitempty"`
	RiskLevel   *string `json:"risk_level,omitempty"`
	Status      *string `json:"status,omitempty"`
}

// Valid status transitions: map[current_status] -> []allowed_next_statuses
var validStatusTransitions = map[string][]string{
	"backlog":     {"spec_review", "cancelled"},
	"spec_review": {"approved", "backlog", "cancelled"},
	"approved":    {"running", "cancelled"},
	"running":     {"reviewing", "failed", "cancelled"},
	"reviewing":   {"pr_created", "running", "failed", "cancelled"},
	"pr_created":  {"done", "running", "cancelled"},
	"done":        {},
	"failed":      {"backlog", "running", "cancelled"},
	"cancelled":   {"backlog"},
}

func isValidTransition(from, to string) bool {
	allowed, ok := validStatusTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// ListTasks returns all tasks for a project.
func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}

	status := r.URL.Query().Get("status")

	query := `
		SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id,
		       title, description, status, priority, risk_level, target_branch,
		       spec, acceptance_criteria, max_cost, max_runtime_minutes,
		       approval_requirements, metadata, started_at, completed_at, created_at, updated_at
		FROM tasks
		WHERE project_id = $1 AND deleted_at IS NULL
	`
	args := []interface{}{projectID}

	if status != "" {
		query += ` AND status = $2`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := scanTask(rows, &t); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		tasks = append(tasks, t)
	}

	if tasks == nil {
		tasks = []Task{}
	}
	respond.JSON(w, http.StatusOK, tasks)
}

// CreateTask creates a new task.
func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("project id is required"))
		return
	}

	user := auth.UserFromContext(ctx)
	if user == nil {
		respond.Error(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	if req.RepositoryID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("repository_id is required"))
		return
	}
	if req.Title == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("title is required"))
		return
	}

	task, err := h.insertTask(ctx, createTaskOptions{
		ProjectID:    projectID,
		RepositoryID: req.RepositoryID,
		CreatedBy:    user.UserID,
		Source:       "web",
		Title:        req.Title,
		Description:  req.Description,
		Priority:     req.Priority,
		RiskLevel:    req.RiskLevel,
		TargetBranch: req.TargetBranch,
		MaxCost:      req.MaxCost,
		Spec:         req.Spec,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	h.logAgentVaultEvent(ctx, taskCreatedEvent(task, "web"))

	respond.JSON(w, http.StatusCreated, task)
}

// GetTask returns a single task by ID.
func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	var t Task
	err := scanTask(h.db.QueryRowContext(ctx, `
		SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id,
		       title, description, status, priority, risk_level, target_branch,
		       spec, acceptance_criteria, max_cost, max_runtime_minutes,
		       approval_requirements, metadata, started_at, completed_at, created_at, updated_at
		FROM tasks
		WHERE id = $1 AND deleted_at IS NULL
	`, id), &t)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("task not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusOK, t)
}

// UpdateTask updates a task's fields.
func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	var req UpdateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	// Get current task status for validation
	var currentStatus string
	err := h.db.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("task not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Validate status transition if status is being updated
	if req.Status != nil && *req.Status != currentStatus {
		if !isValidTransition(currentStatus, *req.Status) {
			respond.Error(w, http.StatusBadRequest, errors.New("invalid status transition from "+currentStatus+" to "+*req.Status))
			return
		}
	}

	now := time.Now().UTC()
	_, err = h.db.ExecContext(ctx, `
		UPDATE tasks SET
			title = COALESCE($1, title),
			description = COALESCE($2, description),
			priority = COALESCE($3, priority),
			risk_level = COALESCE($4, risk_level),
			status = COALESCE($5, status),
			updated_at = $6
		WHERE id = $7 AND deleted_at IS NULL
	`, req.Title, req.Description, req.Priority, req.RiskLevel, req.Status, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	h.GetTask(w, r)
}

// ApproveSpec transitions a task from spec_review to approved.
func (h *Handler) ApproveSpec(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	now := time.Now().UTC()
	result, err := h.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'approved', updated_at = $1
		WHERE id = $2 AND status = 'spec_review' AND deleted_at IS NULL
	`, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		respond.Error(w, http.StatusBadRequest, errors.New("task not in spec_review status or not found"))
		return
	}

	if h.eventBus != nil {
		data, _ := json.Marshal(events.TaskEvent{
			TaskID: id,
			Status: "approved",
		})
		if pubErr := h.eventBus.Publish(events.TaskApproved, data); pubErr != nil {
			h.logger.Warn("failed to publish task approved event", "task_id", id, "error", pubErr)
		}
	}

	respond.JSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// CancelTask transitions a task to cancelled status.
func (h *Handler) CancelTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	now := time.Now().UTC()
	result, err := h.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'cancelled', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL AND status NOT IN ('done', 'cancelled')
	`, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		respond.Error(w, http.StatusBadRequest, errors.New("task cannot be cancelled or not found"))
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

type taskScanner interface {
	Scan(dest ...any) error
}

// scanTask scans a task row into a Task struct.
func scanTask(scanner taskScanner, t *Task) error {
	var workspaceID, sourceID, description, spec, acceptanceCriteria, approvalRequirements, metadata sql.NullString
	var maxCost sql.NullFloat64
	var startedAt, completedAt sql.NullTime
	if err := scanner.Scan(
		&t.ID, &t.ProjectID, &t.RepositoryID, &workspaceID, &t.CreatedBy, &t.Source, &sourceID,
		&t.Title, &description, &t.Status, &t.Priority, &t.RiskLevel, &t.TargetBranch,
		&spec, &acceptanceCriteria, &maxCost, &t.MaxRuntimeMinutes,
		&approvalRequirements, &metadata, &startedAt, &completedAt, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return err
	}
	if workspaceID.Valid {
		t.WorkspaceID = &workspaceID.String
	}
	if sourceID.Valid {
		t.SourceID = &sourceID.String
	}
	if description.Valid {
		t.Description = &description.String
	}
	if spec.Valid {
		t.Spec = json.RawMessage(spec.String)
	}
	if acceptanceCriteria.Valid {
		t.AcceptanceCriteria = json.RawMessage(acceptanceCriteria.String)
	}
	if maxCost.Valid {
		t.MaxCost = &maxCost.Float64
	}
	if approvalRequirements.Valid {
		t.ApprovalRequirements = json.RawMessage(approvalRequirements.String)
	}
	if metadata.Valid {
		t.Metadata = json.RawMessage(metadata.String)
	}
	if startedAt.Valid {
		t.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		t.CompletedAt = &completedAt.Time
	}
	return nil
}

func (h *Handler) insertTask(ctx context.Context, opts createTaskOptions) (Task, error) {
	priority := opts.Priority
	if priority == "" {
		priority = "medium"
	}
	riskLevel := opts.RiskLevel
	if riskLevel == "" {
		riskLevel = "low"
	}
	targetBranch := opts.TargetBranch
	if targetBranch == "" {
		targetBranch = "main"
	}
	source := opts.Source
	if source == "" {
		source = "web"
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	var desc interface{}
	if opts.Description != "" {
		desc = opts.Description
	}
	var spec interface{}
	if len(opts.Spec) > 0 {
		spec = opts.Spec
	}
	metadata := json.RawMessage(`{}`)
	if len(opts.Metadata) > 0 {
		metadata = opts.Metadata
	}

	_, err := h.db.ExecContext(ctx, `
		INSERT INTO tasks (id, project_id, repository_id, created_by, source, source_id, title,
			description, status, priority, risk_level, target_branch, spec,
			acceptance_criteria, max_cost, max_runtime_minutes, approval_requirements, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'backlog', $9, $10, $11, $12,
			'[]', $13, 60, '[]', $14, $15, $15)
	`, id, opts.ProjectID, opts.RepositoryID, opts.CreatedBy, source, opts.SourceID, opts.Title, desc, priority, riskLevel, targetBranch, spec, opts.MaxCost, metadata, now)
	if err != nil {
		return Task{}, err
	}

	task := Task{
		ID:                id,
		ProjectID:         opts.ProjectID,
		RepositoryID:      opts.RepositoryID,
		CreatedBy:         opts.CreatedBy,
		Source:            source,
		SourceID:          opts.SourceID,
		Title:             opts.Title,
		Status:            "backlog",
		Priority:          priority,
		RiskLevel:         riskLevel,
		TargetBranch:      targetBranch,
		Spec:              opts.Spec,
		Metadata:          metadata,
		MaxCost:           opts.MaxCost,
		MaxRuntimeMinutes: 60,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if opts.Description != "" {
		desc := opts.Description
		task.Description = &desc
	}

	return task, nil
}
