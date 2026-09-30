-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS work_items (
    id          TEXT PRIMARY KEY,
    repository  TEXT NOT NULL,
    state       TEXT NOT NULL,
    claimed_by  TEXT,
    lease_until TIMESTAMPTZ,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_work_items_repository_state ON work_items(repository, state);
CREATE INDEX IF NOT EXISTS idx_work_items_lease_until ON work_items(lease_until);

CREATE TABLE IF NOT EXISTS evidence_bundles (
    work_item_id TEXT NOT NULL,
    head_sha     TEXT NOT NULL,
    base_sha     TEXT NOT NULL,
    payload      JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (work_item_id, head_sha)
);

CREATE INDEX IF NOT EXISTS idx_evidence_bundles_work_item_id ON evidence_bundles(work_item_id);
CREATE INDEX IF NOT EXISTS idx_evidence_bundles_head_sha ON evidence_bundles(head_sha);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_evidence_bundles_head_sha;
DROP INDEX IF EXISTS idx_evidence_bundles_work_item_id;
DROP TABLE IF EXISTS evidence_bundles;
DROP INDEX IF EXISTS idx_work_items_lease_until;
DROP INDEX IF EXISTS idx_work_items_repository_state;
DROP TABLE IF EXISTS work_items;
-- +goose StatementEnd
