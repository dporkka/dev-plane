-- name: GetApproval :one
SELECT * FROM approvals
WHERE id = $1
LIMIT 1;

-- name: ListApprovalsByTask :many
SELECT * FROM approvals
WHERE task_id = $1
ORDER BY created_at DESC;

-- name: ListPendingApprovalsByTask :many
SELECT * FROM approvals
WHERE task_id = $1 AND response IS NULL
ORDER BY created_at DESC;

-- name: ListApprovalsByAgentRun :many
SELECT * FROM approvals
WHERE agent_run_id = $1
ORDER BY created_at DESC;

-- name: CreateApproval :one
INSERT INTO approvals (
    id, task_id, agent_run_id, approval_type, requested_by,
    requested_at, expires_at, metadata, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

-- name: UpdateApprovalResponse :one
UPDATE approvals
SET
    response = $2,
    response_note = $3,
    responded_by = $4,
    responded_at = $5,
    updated_at = $6
WHERE id = $1
RETURNING *;

-- name: DeleteApproval :exec
DELETE FROM approvals
WHERE id = $1;
