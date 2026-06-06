-- name: GetProject :one
SELECT * FROM projects
WHERE id = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: GetProjectBySlug :one
SELECT * FROM projects
WHERE organization_id = $1 AND slug = $2 AND deleted_at IS NULL
LIMIT 1;

-- name: ListProjectsByOrganization :many
SELECT * FROM projects
WHERE organization_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: CreateProject :one
INSERT INTO projects (
    id, organization_id, name, slug, description, settings, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects
SET
    name = COALESCE($2, name),
    slug = COALESCE($3, slug),
    description = COALESCE($4, description),
    settings = COALESCE($5, settings),
    updated_at = $6
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteProject :exec
UPDATE projects
SET deleted_at = $2, updated_at = $3
WHERE id = $1 AND deleted_at IS NULL;

-- name: HardDeleteProject :exec
DELETE FROM projects
WHERE id = $1;
