-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS artifact_uploads (
    id                         UUID PRIMARY KEY,
    organization_id            UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    workspace_id               UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    logical_path               TEXT NOT NULL,
    media_type                 TEXT NOT NULL DEFAULT 'application/octet-stream',
    expected_digest_algorithm  TEXT NOT NULL,
    expected_digest_hex        TEXT NOT NULL,
    expected_size              BIGINT NOT NULL,
    staging_key                TEXT NOT NULL,
    provider_upload_id         TEXT NOT NULL,
    part_size                  BIGINT NOT NULL,
    part_count                 INTEGER NOT NULL,
    status                     TEXT NOT NULL DEFAULT 'initiated',
    initiated_by               UUID NOT NULL REFERENCES users(id),
    artifact_id                UUID REFERENCES artifacts(id) ON DELETE SET NULL,
    error_message              TEXT,
    expires_at                 TIMESTAMPTZ NOT NULL,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (expected_size >= 0),
    CHECK (part_size > 0),
    CHECK (part_count > 0 AND part_count <= 10000)
);

CREATE INDEX IF NOT EXISTS idx_artifact_uploads_workspace
    ON artifact_uploads(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_artifact_uploads_status
    ON artifact_uploads(status, expires_at);
CREATE INDEX IF NOT EXISTS idx_artifact_uploads_initiated_by
    ON artifact_uploads(initiated_by);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_artifact_uploads_initiated_by;
DROP INDEX IF EXISTS idx_artifact_uploads_status;
DROP INDEX IF EXISTS idx_artifact_uploads_workspace;
DROP TABLE IF EXISTS artifact_uploads;
-- +goose StatementEnd
