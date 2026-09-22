-- +goose Up
-- +goose StatementBegin
ALTER TABLE pull_requests ADD COLUMN reviewed_commit_id TEXT;
ALTER TABLE pull_requests ADD COLUMN published_commit_id TEXT;
ALTER TABLE pull_requests ADD COLUMN target_head_id TEXT;
ALTER TABLE pull_requests ADD COLUMN verification JSONB DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_pull_requests_published_commit_id
    ON pull_requests(published_commit_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_pull_requests_published_commit_id;
ALTER TABLE pull_requests DROP COLUMN verification;
ALTER TABLE pull_requests DROP COLUMN target_head_id;
ALTER TABLE pull_requests DROP COLUMN published_commit_id;
ALTER TABLE pull_requests DROP COLUMN reviewed_commit_id;
-- +goose StatementEnd
