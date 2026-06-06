-- name: GetAgentRun :one
SELECT * FROM agent_runs
WHERE id = $1
LIMIT 1;

-- name: ListAgentRunsByTask :many
SELECT * FROM agent_runs
WHERE task_id = $1
ORDER BY created_at DESC;

-- name: ListAgentRunsByWorkspace :many
SELECT * FROM agent_runs
WHERE workspace_id = $1
ORDER BY created_at DESC;

-- name: ListAgentRunsByStatus :many
SELECT * FROM agent_runs
WHERE status = $1
ORDER BY created_at DESC;

-- name: CreateAgentRun :one
INSERT INTO agent_runs (
    id, task_id, workspace_id, agent_role, model, provider,
    status, prompt_tokens, completion_tokens, total_cost,
    error_message, summary, metadata, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
)
RETURNING *;

-- name: UpdateAgentRun :one
UPDATE agent_runs
SET
    agent_role = COALESCE($2, agent_role),
    model = COALESCE($3, model),
    provider = COALESCE($4, provider),
    status = COALESCE($5, status),
    error_message = COALESCE($6, error_message),
    summary = COALESCE($7, summary),
    metadata = COALESCE($8, metadata),
    updated_at = $9
WHERE id = $1
RETURNING *;

-- name: UpdateAgentRunStatus :one
UPDATE agent_runs
SET
    status = $2,
    started_at = COALESCE($3, started_at),
    completed_at = COALESCE($4, completed_at),
    updated_at = $5
WHERE id = $1
RETURNING *;

-- name: UpdateAgentRunTokens :one
UPDATE agent_runs
SET
    prompt_tokens = $2,
    completion_tokens = $3,
    total_cost = $4,
    updated_at = $5
WHERE id = $1
RETURNING *;

-- name: DeleteAgentRun :exec
DELETE FROM agent_runs
WHERE id = $1;
