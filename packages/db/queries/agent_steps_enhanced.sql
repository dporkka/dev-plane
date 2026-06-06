-- Agent steps enhanced queries for Phase 2 Agent Runner
-- These complement the existing agent_steps queries in agent_runs.sql

-- name: ListAgentStepsByRun :many
-- Returns all steps for a given agent run, ordered by step number.
SELECT * FROM agent_steps WHERE agent_run_id = $1 ORDER BY step_number ASC;

-- name: GetLatestAgentStep :one
-- Returns the most recent step for a given agent run.
SELECT * FROM agent_steps WHERE agent_run_id = $1 ORDER BY step_number DESC LIMIT 1;

-- name: CountAgentStepsByRun :one
-- Returns step count and total cost for a given agent run.
SELECT COUNT(*) as count, COALESCE(SUM(cost), 0) as total_cost FROM agent_steps WHERE agent_run_id = $1;

-- name: UpdateAgentStepStatus :exec
-- Updates the status, output, exit code, and latency of a step.
UPDATE agent_steps SET status = $1, tool_output = $2, exit_code = $3, latency_ms = $4 WHERE id = $5;

-- name: CreateAgentStep :one
-- Inserts a new agent step.
INSERT INTO agent_steps (
    id, agent_run_id, step_number, step_type, status, content,
    tool_name, tool_input, tool_output, command, command_output,
    exit_code, file_path, diff, cost, latency_ms, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
)
RETURNING *;
