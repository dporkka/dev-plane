package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// WorkspaceStatus constants.
const (
	WorkspaceStatusPending    = "pending"
	WorkspaceStatusPreparing  = "preparing"
	WorkspaceStatusReady      = "ready"
	WorkspaceStatusRunning    = "running"
	WorkspaceStatusStopped    = "stopped"
	WorkspaceStatusError      = "error"
	WorkspaceStatusDestroyed  = "destroyed"
)

// Workspace represents an isolated development environment for a task.
type Workspace struct {
	ID               string          `json:"id"`
	RepositoryID     string          `json:"repository_id"`
	TaskID           *string         `json:"task_id,omitempty"`
	Name             string          `json:"name"`
	Branch           string          `json:"branch"`
	BaseBranch       string          `json:"base_branch"`
	WorktreePath     *string         `json:"worktree_path,omitempty"`
	RuntimeProvider  string          `json:"runtime_provider"`
	RuntimeSessionID *string         `json:"runtime_session_id,omitempty"`
	Status           string          `json:"status"`
	PreviewURL       *string         `json:"preview_url,omitempty"`
	Settings         json.RawMessage `json:"settings,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	DeletedAt        *time.Time      `json:"deleted_at,omitempty"`
}

// Validate checks that the workspace has required fields.
func (w *Workspace) Validate() error {
	if w.Name == "" {
		return errors.New("workspace name is required")
	}
	if w.Branch == "" {
		return errors.New("workspace branch is required")
	}
	if w.RepositoryID == "" {
		return errors.New("workspace repository_id is required")
	}
	return nil
}

// NullWorkspace returns a Workspace from sql.Null fields.
func NullWorkspace(id sql.NullString, repoID sql.NullString, taskID sql.NullString, name sql.NullString, branch sql.NullString, baseBranch sql.NullString, worktreePath sql.NullString, runtimeProvider sql.NullString, runtimeSessionID sql.NullString, status sql.NullString, previewURL sql.NullString, settings sql.NullString, createdAt sql.NullTime, updatedAt sql.NullTime, deletedAt sql.NullTime) *Workspace {
	w := &Workspace{}
	if id.Valid {
		w.ID = id.String
	}
	if repoID.Valid {
		w.RepositoryID = repoID.String
	}
	if taskID.Valid {
		t := taskID.String
		w.TaskID = &t
	}
	if name.Valid {
		w.Name = name.String
	}
	if branch.Valid {
		w.Branch = branch.String
	}
	if baseBranch.Valid {
		w.BaseBranch = baseBranch.String
	}
	if worktreePath.Valid {
		wp := worktreePath.String
		w.WorktreePath = &wp
	}
	if runtimeProvider.Valid {
		w.RuntimeProvider = runtimeProvider.String
	}
	if runtimeSessionID.Valid {
		rs := runtimeSessionID.String
		w.RuntimeSessionID = &rs
	}
	if status.Valid {
		w.Status = status.String
	}
	if previewURL.Valid {
		pu := previewURL.String
		w.PreviewURL = &pu
	}
	if settings.Valid {
		w.Settings = json.RawMessage(settings.String)
	}
	if createdAt.Valid {
		w.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		w.UpdatedAt = updatedAt.Time
	}
	if deletedAt.Valid {
		w.DeletedAt = &deletedAt.Time
	}
	return w
}
