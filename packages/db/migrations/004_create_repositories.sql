-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS repositories (
    id                  UUID PRIMARY KEY,
    project_id          UUID NOT NULL REFERENCES projects(id),
    github_id           BIGINT,
    owner               TEXT NOT NULL,
    name                TEXT NOT NULL,
    full_name           TEXT NOT NULL,
    clone_url           TEXT NOT NULL,
    default_branch      TEXT NOT NULL DEFAULT 'main',
    private             BOOLEAN NOT NULL DEFAULT false,
    connection_status   TEXT NOT NULL DEFAULT 'pending',
    last_synced_at      TIMESTAMPTZ,
    webhook_secret      TEXT,
    settings            JSONB DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at          TIMESTAMPTZ,
    UNIQUE(project_id, full_name)
);

CREATE INDEX IF NOT EXISTS idx_repositories_project_id ON repositories(project_id);
CREATE INDEX IF NOT EXISTS idx_repositories_full_name ON repositories(full_name);
CREATE INDEX IF NOT EXISTS idx_repositories_connection_status ON repositories(connection_status);
CREATE INDEX IF NOT EXISTS idx_repositories_deleted_at ON repositories(deleted_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_repositories_deleted_at;
DROP INDEX IF EXISTS idx_repositories_connection_status;
DROP INDEX IF EXISTS idx_repositories_full_name;
DROP INDEX IF EXISTS idx_repositories_project_id;
DROP TABLE IF EXISTS repositories;
-- +goose StatementEnd
