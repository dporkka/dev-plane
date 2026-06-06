-- name: GetRepository :one
SELECT * FROM repositories
WHERE id = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: GetRepositoryByFullName :one
SELECT * FROM repositories
WHERE project_id = $1 AND full_name = $2 AND deleted_at IS NULL
LIMIT 1;

-- name: ListRepositoriesByProject :many
SELECT * FROM repositories
WHERE project_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: CreateRepository :one
INSERT INTO repositories (
    id, project_id, github_id, owner, name, full_name, clone_url,
    default_branch, private, connection_status, webhook_secret,
    settings, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
RETURNING *;

-- name: UpdateRepository :one
UPDATE repositories
SET
    github_id = COALESCE($2, github_id),
    owner = COALESCE($3, owner),
    name = COALESCE($4, name),
    full_name = COALESCE($5, full_name),
    clone_url = COALESCE($6, clone_url),
    default_branch = COALESCE($7, default_branch),
    private = COALESCE($8, private),
    connection_status = COALESCE($9, connection_status),
    last_synced_at = COALESCE($10, last_synced_at),
    webhook_secret = COALESCE($11, webhook_secret),
    settings = COALESCE($12, settings),
    updated_at = $13
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateRepositoryConnectionStatus :one
UPDATE repositories
SET connection_status = $2, last_synced_at = $3, updated_at = $4
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteRepository :exec
UPDATE repositories
SET deleted_at = $2, updated_at = $3
WHERE id = $1 AND deleted_at IS NULL;

-- name: HardDeleteRepository :exec
DELETE FROM repositories
WHERE id = $1;
