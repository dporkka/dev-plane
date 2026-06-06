package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/respond"
)

// DashboardStats represents the aggregated dashboard statistics.
type DashboardStats struct {
	ActiveRuns       int             `json:"active_runs"`
	PendingApprovals int             `json:"pending_approvals"`
	RecentTasks      []DashboardTask `json:"recent_tasks"`
	RecentFailures   []DashboardTask `json:"recent_failures"`
}

// DashboardTask represents a simplified task for dashboard display.
type DashboardTask struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Priority  string    `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
}

// DashboardAgentRun represents a simplified agent run for dashboard display.
type DashboardAgentRun struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	AgentRole string    `json:"agent_role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// GetDashboard returns aggregated dashboard statistics for an organization.
func (h *Handler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := chi.URLParam(r, "orgID")
	if orgID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("organization id is required"))
		return
	}

	stats := DashboardStats{}

	// Count active runs (status = running or pending)
	err := h.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE p.organization_id = $1 AND ar.status IN ('pending', 'running')
	`, orgID).Scan(&stats.ActiveRuns)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Count pending approvals
	err = h.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM approvals a
		JOIN tasks t ON t.id = a.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE p.organization_id = $1 AND a.responded_at IS NULL
	`, orgID).Scan(&stats.PendingApprovals)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Recent tasks (last 10)
	rows, err := h.db.QueryContext(ctx, `
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
	defer rows.Close()

	for rows.Next() {
		var t DashboardTask
		if err := rows.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.CreatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		stats.RecentTasks = append(stats.RecentTasks, t)
	}

	// Recent failures (last 10 failed tasks)
	rows2, err := h.db.QueryContext(ctx, `
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
	defer rows2.Close()

	for rows2.Next() {
		var t DashboardTask
		if err := rows2.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.CreatedAt); err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		stats.RecentFailures = append(stats.RecentFailures, t)
	}

	respond.JSON(w, http.StatusOK, stats)
}
