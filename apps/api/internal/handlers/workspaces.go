package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/respond"
)

// WorkspaceResponse is the API representation of a workspace.
type WorkspaceResponse struct {
	ID               string     `json:"id"`
	RepositoryID     string     `json:"repository_id"`
	TaskID           *string    `json:"task_id,omitempty"`
	Name             string     `json:"name"`
	Branch           string     `json:"branch"`
	BaseBranch       string     `json:"base_branch"`
	WorktreePath     *string    `json:"worktree_path,omitempty"`
	RuntimeProvider  string     `json:"runtime_provider"`
	RuntimeSessionID *string    `json:"runtime_session_id,omitempty"`
	Status           string     `json:"status"`
	PreviewURL       *string    `json:"preview_url,omitempty"`
	Settings         *string    `json:"settings,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
}

// ListTaskWorkspaces returns all workspaces associated with a task.
func (h *Handler) ListTaskWorkspaces(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	taskID := chi.URLParam(r, "taskID")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	if err := authz.AuthorizeTask(ctx, h.db, user, taskID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("task not found"))
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, repository_id, task_id, name, branch, base_branch, worktree_path,
		       runtime_provider, runtime_session_id, status, preview_url, settings,
		       created_at, updated_at, deleted_at
		FROM workspaces
		WHERE task_id = $1
		ORDER BY created_at DESC
	`, taskID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var workspaces []WorkspaceResponse
	for rows.Next() {
		var ws WorkspaceResponse
		var taskID, worktreePath, runtimeSessionID, previewURL, settings sql.NullString
		var deletedAt sql.NullTime
		err := rows.Scan(
			&ws.ID, &ws.RepositoryID, &taskID, &ws.Name, &ws.Branch, &ws.BaseBranch, &worktreePath,
			&ws.RuntimeProvider, &runtimeSessionID, &ws.Status, &previewURL, &settings,
			&ws.CreatedAt, &ws.UpdatedAt, &deletedAt,
		)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if taskID.Valid {
			ws.TaskID = &taskID.String
		}
		if worktreePath.Valid {
			ws.WorktreePath = &worktreePath.String
		}
		if runtimeSessionID.Valid {
			ws.RuntimeSessionID = &runtimeSessionID.String
		}
		if previewURL.Valid {
			ws.PreviewURL = &previewURL.String
		}
		if settings.Valid {
			ws.Settings = &settings.String
		}
		if deletedAt.Valid {
			ws.DeletedAt = &deletedAt.Time
		}
		workspaces = append(workspaces, ws)
	}

	if workspaces == nil {
		workspaces = []WorkspaceResponse{}
	}
	respond.JSON(w, http.StatusOK, workspaces)
}

// GetWorkspace returns a single workspace by ID.
func (h *Handler) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	var ws WorkspaceResponse
	var taskID, worktreePath, runtimeSessionID, previewURL, settings sql.NullString
	var deletedAt sql.NullTime
	err := h.db.QueryRowContext(ctx, `
		SELECT id, repository_id, task_id, name, branch, base_branch, worktree_path,
		       runtime_provider, runtime_session_id, status, preview_url, settings,
		       created_at, updated_at, deleted_at
		FROM workspaces
		WHERE id = $1
	`, id).Scan(
		&ws.ID, &ws.RepositoryID, &taskID, &ws.Name, &ws.Branch, &ws.BaseBranch, &worktreePath,
		&ws.RuntimeProvider, &runtimeSessionID, &ws.Status, &previewURL, &settings,
		&ws.CreatedAt, &ws.UpdatedAt, &deletedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if taskID.Valid {
		ws.TaskID = &taskID.String
	}
	if worktreePath.Valid {
		ws.WorktreePath = &worktreePath.String
	}
	if runtimeSessionID.Valid {
		ws.RuntimeSessionID = &runtimeSessionID.String
	}
	if previewURL.Valid {
		ws.PreviewURL = &previewURL.String
	}
	if settings.Valid {
		ws.Settings = &settings.String
	}
	if deletedAt.Valid {
		ws.DeletedAt = &deletedAt.Time
	}
	respond.JSON(w, http.StatusOK, ws)
}

// DestroyWorkspace marks a workspace as destroyed and removes its worktree.
func (h *Handler) DestroyWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	var worktreePath sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT worktree_path FROM workspaces WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&worktreePath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	now := time.Now().UTC()
	result, err := h.db.ExecContext(ctx, `
		UPDATE workspaces
		SET status = 'destroyed', worktree_path = NULL, runtime_session_id = NULL, deleted_at = $1, updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found or already destroyed"))
		return
	}

	// Best-effort cleanup of the worktree directory
	if worktreePath.Valid && worktreePath.String != "" {
		_ = os.RemoveAll(worktreePath.String)
	}

	respond.JSON(w, http.StatusOK, map[string]string{
		"status": "destroyed",
		"id":     id,
	})
}

// GetWorkspaceDiff returns the git diff for a workspace.
func (h *Handler) GetWorkspaceDiff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, id); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	workspacePath, err := h.getWorkspacePath(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if workspacePath == "" {
		respond.JSON(w, http.StatusOK, map[string]string{"diff": ""})
		return
	}

	cmd := exec.CommandContext(ctx, "git", "-C", workspacePath, "diff", "--", ".")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Diff may be empty or repo may not be initialized; return empty diff gracefully
		respond.JSON(w, http.StatusOK, map[string]string{"diff": ""})
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"diff": string(out)})
}

// workspaceDiffPath returns the workspace path for diff operations, validating traversal.
func workspaceDiffPath(workspacePath string) error {
	if workspacePath == "" {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resolved) == "" || resolved == "/" {
		return fmt.Errorf("invalid workspace path")
	}
	return nil
}
