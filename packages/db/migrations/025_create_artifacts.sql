-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS artifacts (
    id                         UUID PRIMARY KEY,
    organization_id            UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    workspace_id               UUID REFERENCES workspaces(id) ON DELETE CASCADE,
    agent_run_id               UUID REFERENCES agent_runs(id) ON DELETE SET NULL,
    step_id                    UUID REFERENCES agent_steps(id) ON DELETE SET NULL,
    artifact_type              TEXT NOT NULL DEFAULT 'file',
    file_name                  TEXT NOT NULL,
    file_path                  TEXT NOT NULL,
    logical_path               TEXT,
    kind                       TEXT,
    mime_type                  TEXT,
    size_bytes                 BIGINT,
    digest_algorithm           TEXT,
    digest_hex                 TEXT,
    semantic_digest_algorithm  TEXT,
    semantic_digest_hex        TEXT,
    artifact_json              JSONB,
    metadata                   JSONB DEFAULT '{}',
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_artifacts_organization_id
    ON artifacts(organization_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_workspace_id
    ON artifacts(workspace_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_agent_run_id
    ON artifacts(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_logical_path
    ON artifacts(workspace_id, logical_path);
CREATE INDEX IF NOT EXISTS idx_artifacts_digest
    ON artifacts(digest_algorithm, digest_hex);
CREATE INDEX IF NOT EXISTS idx_artifacts_created_at
    ON artifacts(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_artifacts_created_at;
DROP INDEX IF EXISTS idx_artifacts_digest;
DROP INDEX IF EXISTS idx_artifacts_logical_path;
DROP INDEX IF EXISTS idx_artifacts_agent_run_id;
DROP INDEX IF EXISTS idx_artifacts_workspace_id;
DROP INDEX IF EXISTS idx_artifacts_organization_id;
DROP TABLE IF EXISTS artifacts;
-- +goose StatementEnd
