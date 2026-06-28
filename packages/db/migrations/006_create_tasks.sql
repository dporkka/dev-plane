-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS tasks (
    id                      UUID PRIMARY KEY,
    project_id              UUID NOT NULL REFERENCES projects(id),
    repository_id           UUID NOT NULL REFERENCES repositories(id),
    workspace_id            UUID REFERENCES workspaces(id),
    created_by              UUID NOT NULL REFERENCES users(id),
    source                  TEXT NOT NULL DEFAULT 'web',
    source_id               TEXT,
    title                   TEXT NOT NULL,
    description             TEXT,
    status                  TEXT NOT NULL DEFAULT 'backlog',
    priority                TEXT NOT NULL DEFAULT 'medium',
    risk_level              TEXT NOT NULL DEFAULT 'low',
    target_branch           TEXT NOT NULL DEFAULT 'main',
    spec                    JSONB,
    acceptance_criteria     JSONB DEFAULT '[]',
    max_cost                DECIMAL(10,4),
    max_runtime_minutes     INTEGER DEFAULT 60,
    approval_requirements   JSONB DEFAULT '[]',
    metadata                JSONB DEFAULT '{}',
    started_at              TIMESTAMPTZ,
    completed_at            TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at              TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_tasks_project_id ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_repository_id ON tasks(repository_id);
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_id ON tasks(workspace_id);
CREATE INDEX IF NOT EXISTS idx_tasks_created_by ON tasks(created_by);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks(priority);
CREATE INDEX IF NOT EXISTS idx_tasks_created_at ON tasks(created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_deleted_at ON tasks(deleted_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tasks_deleted_at;
DROP INDEX IF EXISTS idx_tasks_created_at;
DROP INDEX IF EXISTS idx_tasks_priority;
DROP INDEX IF EXISTS idx_tasks_status;
DROP INDEX IF EXISTS idx_tasks_created_by;
DROP INDEX IF EXISTS idx_tasks_workspace_id;
DROP INDEX IF EXISTS idx_tasks_repository_id;
DROP INDEX IF EXISTS idx_tasks_project_id;
DROP TABLE IF EXISTS tasks;
-- +goose StatementEnd
