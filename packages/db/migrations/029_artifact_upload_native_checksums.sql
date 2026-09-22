-- +goose Up
-- +goose StatementBegin
ALTER TABLE artifact_uploads ADD COLUMN native_checksum_algorithm TEXT;
ALTER TABLE artifact_uploads ADD COLUMN native_checksum_base64 TEXT;
ALTER TABLE artifact_uploads ADD COLUMN native_checksum_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE artifact_uploads ADD COLUMN verification_mode TEXT NOT NULL DEFAULT 'stream_sha256';

CREATE INDEX IF NOT EXISTS idx_artifact_uploads_native_checksum
    ON artifact_uploads(native_checksum_enabled, status, updated_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_artifact_uploads_native_checksum;
-- SQLite cannot drop added columns without rebuilding the table.
-- +goose StatementEnd
