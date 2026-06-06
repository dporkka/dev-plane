-- name: GetWorkspace :one
SELECT * FROM workspaces
WHERE id = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: ListWorkspacesByRepository :many
SELECT * FROM workspaces
WHERE repository_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: ListWorkspacesByTask :many
SELECT * FROM workspaces
WHERE task_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: ListWorkspacesByStatus :many
SELECT * FROM workspaces
WHERE status = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: CreateWorkspace :one
INSERT INTO workspaces (
    id, repository_id, task_id, name, branch, base_branch,
    worktree_path, runtime_provider, runtime_session_id,
    status, preview_url, settings, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
RETURNING *;

-- name: UpdateWorkspace :one
UPDATE workspaces
SET
    name = COALESCE($2, name),
    branch = COALESCE($3, branch),
    base_branch = COALESCE($4, base_branch),
    worktree_path = COALESCE($5, worktree_path),
    runtime_provider = COALESCE($6, runtime_provider),
    runtime_session_id = COALESCE($7, runtime_session_id),
    preview_url = COALESCE($8, preview_url),
    settings = COALESCE($9, settings),
    updated_at = $10
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateWorkspaceStatus :one
UPDATE workspaces
SET status = $2, updated_at = $3
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteWorkspace :exec
UPDATE workspaces
SET deleted_at = $2, updated_at = $3
WHERE id = $1 AND deleted_at IS NULL;

-- name: HardDeleteWorkspace :exec
DELETE FROM workspaces
WHERE id = $1;
