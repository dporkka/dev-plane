-- name: GetAuditLog :one
SELECT * FROM audit_logs
WHERE id = $1
LIMIT 1;

-- name: ListAuditLogsByOrganization :many
SELECT * FROM audit_logs
WHERE organization_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditLogsByActor :many
SELECT * FROM audit_logs
WHERE actor_id = $1
ORDER BY created_at DESC;

-- name: ListAuditLogsByResource :many
SELECT * FROM audit_logs
WHERE resource_type = $1 AND resource_id = $2
ORDER BY created_at DESC;

-- name: CreateAuditLog :one
INSERT INTO audit_logs (
    id, organization_id, actor_type, actor_id, action,
    resource_type, resource_id, details, ip_address, user_agent, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: CountAuditLogsByOrganization :one
SELECT COUNT(*) FROM audit_logs
WHERE organization_id = $1;
