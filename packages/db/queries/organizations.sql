-- name: GetOrganization :one
SELECT * FROM organizations
WHERE id = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: GetOrganizationBySlug :one
SELECT * FROM organizations
WHERE slug = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: ListOrganizations :many
SELECT * FROM organizations
WHERE deleted_at IS NULL
ORDER BY created_at DESC;

-- name: CreateOrganization :one
INSERT INTO organizations (
    id, name, slug, plan, settings, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: UpdateOrganization :one
UPDATE organizations
SET
    name = COALESCE($2, name),
    slug = COALESCE($3, slug),
    plan = COALESCE($4, plan),
    settings = COALESCE($5, settings),
    updated_at = $6
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteOrganization :exec
UPDATE organizations
SET deleted_at = $2, updated_at = $3
WHERE id = $1 AND deleted_at IS NULL;

-- name: HardDeleteOrganization :exec
DELETE FROM organizations
WHERE id = $1;
