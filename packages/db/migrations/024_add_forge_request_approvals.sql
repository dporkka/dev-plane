-- +goose Up
-- +goose StatementBegin
ALTER TABLE approvals ADD COLUMN forge_request_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_approvals_forge_request_id
    ON approvals(forge_request_id)
    WHERE forge_request_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_approvals_forge_request_id;
ALTER TABLE approvals DROP COLUMN forge_request_id;
-- +goose StatementEnd
