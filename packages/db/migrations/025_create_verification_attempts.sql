-- +goose Up
-- +goose StatementBegin
-- Append-only through the application API; production DB roles should also deny UPDATE and DELETE.
CREATE TABLE IF NOT EXISTS verification_attempts (
    id                   TEXT PRIMARY KEY,
    work_item_id         TEXT NOT NULL,
    repository           TEXT NOT NULL,
    source_head_sha      TEXT NOT NULL,
    target_base_sha      TEXT NOT NULL,
    candidate_commit_sha TEXT NOT NULL,
    policy_digest        TEXT NOT NULL,
    payload              JSONB NOT NULL,
    completed_at         TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_verification_attempts_work_item
    ON verification_attempts(work_item_id);
CREATE INDEX IF NOT EXISTS idx_verification_attempts_candidate
    ON verification_attempts(repository, candidate_commit_sha);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_verification_attempts_candidate;
DROP INDEX IF EXISTS idx_verification_attempts_work_item;
DROP TABLE IF EXISTS verification_attempts;
-- +goose StatementEnd
