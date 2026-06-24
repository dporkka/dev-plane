package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// DashboardStats represents the aggregated dashboard statistics.
type DashboardStats struct {
	ActiveRuns       int `json:"active_runs"`
	TasksToday       int `json:"tasks_today"`
	CostToday        float64 `json:"cost_today"`
	PendingApprovals int `json:"pending_approvals"`
}

// DashboardAgentRun represents a simplified agent run for dashboard display.
type DashboardAgentRun struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	AgentRole string    `json:"agent_role"`
	Model     *string   `json:"model,omitempty"`
	Status    string    `json:"status"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// DashboardTask represents a simplified task for dashboard display.
type DashboardTask struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Priority  string    `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
}

// DashboardData is the response shape expected by the frontend.
type DashboardData struct {
	Stats          DashboardStats      `json:"stats"`
	ActiveRuns     []DashboardAgentRun `json:"active_runs"`
	RecentTasks    []DashboardTask     `json:"recent_tasks"`
	RecentFailures []DashboardTask     `json:"recent_failures"`
}

// GetDashboard returns aggregated dashboard statistics for an organization.
func (h *Handler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	if err := authz.AuthorizeOrganization(ctx, h.db, user, orgID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("organization not found"))
		return
	}

	data := DashboardData{}

	// Active runs
	rows, err := h.db.QueryContext(ctx, `
		SELECT ar.id, ar.task_id, ar.agent_role, ar.model, ar.status, ar.started_at, ar.created_at
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE p.organization_id = $1 AND ar.status IN ('pending', 'running')
		ORDER BY ar.created_at DESC
		LIMIT 20
	`, orgID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var run DashboardAgentRun
		var model sql.NullString
		var startedAt sql.NullTime
		if err := rows.Scan(&run.ID, &run.TaskID, &run.AgentRole, &model, &run.Status, &startedAt, &run.CreatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if model.Valid {
			run.Model = &model.String
		}
		if startedAt.Valid {
			run.StartedAt = &startedAt.Time
		}
		data.ActiveRuns = append(data.ActiveRuns, run)
	}
	if err := rows.Err(); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Pending approvals
	var pendingApprovals int
	err = h.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM approvals a
		JOIN tasks t ON t.id = a.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE p.organization_id = $1 AND a.responded_at IS NULL
	`, orgID).Scan(&pendingApprovals)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Tasks created today
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var tasksToday int
	err = h.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		WHERE p.organization_id = $1 AND t.created_at >= $2 AND t.deleted_at IS NULL
	`, orgID, today).Scan(&tasksToday)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Cost today
	var costToday sql.NullFloat64
	err = h.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(ar.total_cost), 0)
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE p.organization_id = $1 AND ar.created_at >= $2
	`, orgID, today).Scan(&costToday)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	data.Stats = DashboardStats{
		ActiveRuns:       len(data.ActiveRuns),
		TasksToday:       tasksToday,
		CostToday:        costToday.Float64,
		PendingApprovals: pendingApprovals,
	}

	// Recent tasks (last 10)
	rows2, err := h.db.QueryContext(ctx, `
		SELECT t.id, t.title, t.status, t.priority, t.created_at
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		WHERE p.organization_id = $1 AND t.deleted_at IS NULL
		ORDER BY t.created_at DESC
		LIMIT 10
	`, orgID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows2.Close()

	for rows2.Next() {
		var t DashboardTask
		if err := rows2.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.CreatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		data.RecentTasks = append(data.RecentTasks, t)
	}
	if err := rows2.Err(); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Recent failures (last 10 failed tasks)
	rows3, err := h.db.QueryContext(ctx, `
		SELECT t.id, t.title, t.status, t.priority, t.created_at
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		WHERE p.organization_id = $1 AND t.status = 'failed' AND t.deleted_at IS NULL
		ORDER BY t.updated_at DESC
		LIMIT 10
	`, orgID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows3.Close()

	for rows3.Next() {
		var t DashboardTask
		if err := rows3.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.CreatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		data.RecentFailures = append(data.RecentFailures, t)
	}
	if err := rows3.Err(); err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	respond.JSON(w, http.StatusOK, data)
}
