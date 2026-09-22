-- +goose Up
-- +goose StatementBegin
ALTER TABLE artifact_uploads ADD COLUMN reconciliation_claim TEXT;
ALTER TABLE artifact_uploads ADD COLUMN reconciliation_claim_expires_at TIMESTAMPTZ;
ALTER TABLE artifact_uploads ADD COLUMN reconciliation_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE artifact_uploads ADD COLUMN reconciled_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_artifact_uploads_reconciliation
    ON artifact_uploads(status, reconciliation_claim_expires_at, updated_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_artifact_uploads_reconciliation;
-- SQLite cannot drop added columns without rebuilding the table.
-- +goose StatementEnd
