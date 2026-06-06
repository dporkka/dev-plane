package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/runtimes"
)

type runtimeAttacher interface {
	AttachSession(ctx context.Context, sessionID, workspaceID string) (*runtimes.Session, error)
}

func (h *Handler) getRuntimeWorkspace(ctx context.Context, workspaceID string) (*models.Workspace, runtimes.Provider, error) {
	workspace, err := h.loadWorkspaceRuntimeMetadata(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	providerName := strings.ToLower(strings.TrimSpace(workspace.RuntimeProvider))
	if providerName == "" || providerName == "local" || providerName == "unprovisioned" {
		return workspace, nil, nil
	}
	if workspace.RuntimeSessionID == nil || *workspace.RuntimeSessionID == "" {
		return workspace, nil, fmt.Errorf("workspace runtime session id is missing")
	}

	provider, err := h.runtimeProvider(providerName)
	if err != nil {
		return workspace, nil, err
	}
	if attacher, ok := provider.(runtimeAttacher); ok {
		if _, err := attacher.AttachSession(ctx, *workspace.RuntimeSessionID, workspace.ID); err != nil {
			return workspace, nil, fmt.Errorf("attach %s runtime session: %w", providerName, err)
		}
	}
	return workspace, provider, nil
}

func (h *Handler) runtimeProvider(name string) (runtimes.Provider, error) {
	if h.runtimeProviders == nil {
		h.runtimeProviders = map[string]runtimes.Provider{}
	}
	if provider := h.runtimeProviders[name]; provider != nil {
		return provider, nil
	}
	switch name {
	case "docker":
		provider, err := runtimes.NewDockerProvider(workspaceRuntimeBaseDir())
		if err != nil {
			return nil, err
		}
		h.runtimeProviders[name] = provider
		return provider, nil
	default:
		return nil, fmt.Errorf("unsupported workspace runtime provider %q", name)
	}
}

func workspaceRuntimeBaseDir() string {
	if baseDir := os.Getenv("WORKSPACE_BASE_DIR"); baseDir != "" {
		return baseDir
	}
	return filepath.Join(os.TempDir(), "ai-dev-control-plane-workspaces")
}

func (h *Handler) loadWorkspaceRuntimeMetadata(ctx context.Context, workspaceID string) (*models.Workspace, error) {
	var ws models.Workspace
	var taskID, worktreePath, runtimeProvider, runtimeSessionID sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT id, repository_id, task_id, worktree_path, runtime_provider, runtime_session_id, status
		FROM workspaces
		WHERE id = $1 AND deleted_at IS NULL
	`, workspaceID).Scan(
		&ws.ID, &ws.RepositoryID, &taskID, &worktreePath, &runtimeProvider, &runtimeSessionID, &ws.Status,
	)
	if err != nil {
		return nil, err
	}
	if taskID.Valid {
		ws.TaskID = &taskID.String
	}
	if worktreePath.Valid && worktreePath.String != "" {
		ws.WorktreePath = &worktreePath.String
	}
	if runtimeProvider.Valid {
		ws.RuntimeProvider = runtimeProvider.String
	}
	if runtimeSessionID.Valid && runtimeSessionID.String != "" {
		ws.RuntimeSessionID = &runtimeSessionID.String
	}
	return &ws, nil
}
