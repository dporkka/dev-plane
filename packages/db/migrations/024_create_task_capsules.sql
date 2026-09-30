-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS task_capsules (
    agent_run_id    UUID PRIMARY KEY REFERENCES agent_runs(id) ON DELETE CASCADE,
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    workspace_id    UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    version         INTEGER NOT NULL,
    agent_id        TEXT NOT NULL,
    agent_role      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_task_capsules_task_id ON task_capsules(task_id);
CREATE INDEX IF NOT EXISTS idx_task_capsules_workspace_id ON task_capsules(workspace_id);

CREATE TABLE IF NOT EXISTS task_leases (
    agent_run_id    UUID NOT NULL REFERENCES task_capsules(agent_run_id) ON DELETE CASCADE,
    path            TEXT NOT NULL,
    mode            TEXT NOT NULL DEFAULT 'exclusive',
    acquired_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_run_id, path)
);

CREATE INDEX IF NOT EXISTS idx_task_leases_path ON task_leases(path);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_task_leases_path;
DROP TABLE IF EXISTS task_leases;
DROP INDEX IF EXISTS idx_task_capsules_workspace_id;
DROP INDEX IF EXISTS idx_task_capsules_task_id;
DROP TABLE IF EXISTS task_capsules;
-- +goose StatementEnd
