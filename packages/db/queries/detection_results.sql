-- name: CreateDetectionResult :exec
INSERT INTO detection_results (
    id, repository_id, workspace_id, package_manager, framework,
    test_command, lint_command, typecheck_command, dev_command,
    build_command, has_dockerfile, has_devcontainer, raw_output
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: GetDetectionResultsByRepo :many
SELECT * FROM detection_results WHERE repository_id = $1 ORDER BY detected_at DESC;

-- name: GetLatestDetectionResult :one
SELECT * FROM detection_results WHERE repository_id = $1 ORDER BY detected_at DESC LIMIT 1;
