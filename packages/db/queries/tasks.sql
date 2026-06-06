-- name: GetTask :one
SELECT * FROM tasks
WHERE id = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: ListTasksByProject :many
SELECT * FROM tasks
WHERE project_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: ListTasksByStatus :many
SELECT * FROM tasks
WHERE project_id = $1 AND status = $2 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: ListTasksByRepository :many
SELECT * FROM tasks
WHERE repository_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: CreateTask :one
INSERT INTO tasks (
    id, project_id, repository_id, workspace_id, created_by,
    source, source_id, title, description, status, priority,
    risk_level, target_branch, spec, acceptance_criteria,
    max_cost, max_runtime_minutes, approval_requirements, metadata,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
    $15, $16, $17, $18, $19, $20, $21
)
RETURNING *;

-- name: UpdateTask :one
UPDATE tasks
SET
    title = COALESCE($2, title),
    description = COALESCE($3, description),
    status = COALESCE($4, status),
    priority = COALESCE($5, priority),
    risk_level = COALESCE($6, risk_level),
    target_branch = COALESCE($7, target_branch),
    spec = COALESCE($8, spec),
    acceptance_criteria = COALESCE($9, acceptance_criteria),
    max_cost = COALESCE($10, max_cost),
    max_runtime_minutes = COALESCE($11, max_runtime_minutes),
    approval_requirements = COALESCE($12, approval_requirements),
    metadata = COALESCE($13, metadata),
    started_at = COALESCE($14, started_at),
    completed_at = COALESCE($15, completed_at),
    updated_at = $16
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateTaskStatus :one
UPDATE tasks
SET status = $2, updated_at = $3
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteTask :exec
UPDATE tasks
SET deleted_at = $2, updated_at = $3
WHERE id = $1 AND deleted_at IS NULL;

-- name: HardDeleteTask :exec
DELETE FROM tasks
WHERE id = $1;
