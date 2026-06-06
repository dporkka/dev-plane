-- name: GetTaskSpec :one
SELECT * FROM task_specs WHERE task_id = $1 LIMIT 1;

-- name: CreateTaskSpec :exec
INSERT INTO task_specs (
    id, task_id, summary, problem_statement, implementation_plan,
    files_to_change, files_to_create, acceptance_criteria, test_plan,
    risk_assessment, rollback_plan, required_approvals, estimated_cost,
    recommended_agent, generated_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: UpdateTaskSpec :exec
UPDATE task_specs SET
    summary = $2,
    problem_statement = $3,
    implementation_plan = $4,
    files_to_change = $5,
    files_to_create = $6,
    acceptance_criteria = $7,
    test_plan = $8,
    risk_assessment = $9,
    rollback_plan = $10,
    required_approvals = $11
WHERE task_id = $1;

-- name: DeleteTaskSpec :exec
DELETE FROM task_specs WHERE task_id = $1;
