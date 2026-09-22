-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS artifact_leases (
    scope_id       TEXT NOT NULL,
    artifact_path  TEXT NOT NULL,
    owner_id       TEXT NOT NULL,
    token_hash     TEXT NOT NULL,
    generation     BIGINT NOT NULL DEFAULT 1,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (scope_id, artifact_path)
);

CREATE INDEX IF NOT EXISTS idx_artifact_leases_owner
    ON artifact_leases(owner_id);
CREATE INDEX IF NOT EXISTS idx_artifact_leases_expires_at
    ON artifact_leases(expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_artifact_leases_expires_at;
DROP INDEX IF EXISTS idx_artifact_leases_owner;
DROP TABLE IF EXISTS artifact_leases;
-- +goose StatementEnd
