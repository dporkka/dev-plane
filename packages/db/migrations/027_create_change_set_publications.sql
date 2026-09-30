-- +goose Up
-- +goose StatementBegin
ALTER TABLE change_sets
    ADD COLUMN publication_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (publication_status IN ('pending', 'publishing', 'completed', 'blocked'));

ALTER TABLE change_sets ADD COLUMN publication_lease_token TEXT;
ALTER TABLE change_sets ADD COLUMN publication_lease_until TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS change_set_publications (
    change_set_id   UUID NOT NULL REFERENCES change_sets(id) ON DELETE CASCADE,
    candidate_id    UUID NOT NULL REFERENCES change_candidates(id) ON DELETE CASCADE,
    ordinal         INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'publishing', 'merged', 'blocked')),
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    merge_sha       TEXT,
    last_error      TEXT,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (change_set_id, candidate_id),
    UNIQUE (change_set_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_change_set_publications_status
    ON change_set_publications(change_set_id, status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_change_set_publications_status;
DROP TABLE IF EXISTS change_set_publications;
-- SQLite does not support DROP COLUMN on all supported versions; publication_status
-- is intentionally retained on down migration for compatibility.
-- +goose StatementEnd
