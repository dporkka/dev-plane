-- name: GetProjectConfig :one
SELECT * FROM project_configs WHERE repository_id = $1 LIMIT 1;

-- name: UpsertProjectConfig :exec
INSERT INTO project_configs (
    id, repository_id, package_manager, framework, test_command,
    lint_command, typecheck_command, dev_command, build_command,
    has_dockerfile, has_devcontainer
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT(repository_id) DO UPDATE SET
    package_manager = excluded.package_manager,
    framework = excluded.framework,
    test_command = excluded.test_command,
    lint_command = excluded.lint_command,
    typecheck_command = excluded.typecheck_command,
    dev_command = excluded.dev_command,
    build_command = excluded.build_command,
    has_dockerfile = excluded.has_dockerfile,
    has_devcontainer = excluded.has_devcontainer,
    updated_at = CURRENT_TIMESTAMP;

-- name: DeleteProjectConfig :exec
DELETE FROM project_configs WHERE repository_id = $1;
