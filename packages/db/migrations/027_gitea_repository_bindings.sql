-- +goose Up
-- +goose StatementBegin
-- Explicit operator-authorized mapping, never inferred from connection names
-- or incoming webhook receipts. Keep reservations after soft deletion.
CREATE TABLE IF NOT EXISTS gitea_repository_bindings (
    repository_id           UUID PRIMARY KEY REFERENCES repositories(id),
    project_id              UUID NOT NULL REFERENCES projects(id),
    organization_id         UUID NOT NULL REFERENCES organizations(id),
    instance_url            TEXT NOT NULL,
    upstream_repository_id  BIGINT NOT NULL CHECK (upstream_repository_id > 0),
    full_name               TEXT NOT NULL,
    verified_at             TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (instance_url, upstream_repository_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS gitea_repository_bindings;
-- +goose StatementEnd
