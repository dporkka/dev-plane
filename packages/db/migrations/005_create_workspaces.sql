-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS workspaces (
    id                  UUID PRIMARY KEY,
    repository_id       UUID NOT NULL REFERENCES repositories(id),
    task_id             UUID,
    name                TEXT NOT NULL,
    branch              TEXT NOT NULL,
    base_branch         TEXT NOT NULL DEFAULT 'main',
    worktree_path       TEXT,
    runtime_provider    TEXT NOT NULL DEFAULT 'docker',
    runtime_session_id  TEXT,
    status              TEXT NOT NULL DEFAULT 'pending',
    preview_url         TEXT,
    settings            JSONB DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_workspaces_repository_id ON workspaces(repository_id);
CREATE INDEX IF NOT EXISTS idx_workspaces_task_id ON workspaces(task_id);
CREATE INDEX IF NOT EXISTS idx_workspaces_status ON workspaces(status);
CREATE INDEX IF NOT EXISTS idx_workspaces_deleted_at ON workspaces(deleted_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_workspaces_deleted_at;
DROP INDEX IF EXISTS idx_workspaces_status;
DROP INDEX IF EXISTS idx_workspaces_task_id;
DROP INDEX IF EXISTS idx_workspaces_repository_id;
DROP TABLE IF EXISTS workspaces;
-- +goose StatementEnd
