-- name: GetUser :one
SELECT * FROM users
WHERE id = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE organization_id = $1 AND email = $2 AND deleted_at IS NULL
LIMIT 1;

-- name: GetUserByGitHubID :one
SELECT * FROM users
WHERE github_id = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: ListUsersByOrganization :many
SELECT * FROM users
WHERE organization_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: CreateUser :one
INSERT INTO users (
    id, organization_id, email, name, avatar_url, role,
    github_id, github_username, settings, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: UpdateUser :one
UPDATE users
SET
    email = COALESCE($2, email),
    name = COALESCE($3, name),
    avatar_url = COALESCE($4, avatar_url),
    role = COALESCE($5, role),
    github_id = COALESCE($6, github_id),
    github_username = COALESCE($7, github_username),
    settings = COALESCE($8, settings),
    updated_at = $9
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteUser :exec
UPDATE users
SET deleted_at = $2, updated_at = $3
WHERE id = $1 AND deleted_at IS NULL;

-- name: HardDeleteUser :exec
DELETE FROM users
WHERE id = $1;
