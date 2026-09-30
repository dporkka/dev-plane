-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS change_sets (
    id                      UUID PRIMARY KEY,
    project_id              UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                    TEXT NOT NULL,
    description             TEXT,
    status                  TEXT NOT NULL DEFAULT 'draft'
                            CHECK (status IN ('draft', 'authorized', 'completed', 'cancelled')),
    publication_digest      TEXT,
    publication_manifest    JSONB,
    authorized_at           TIMESTAMPTZ,
    authorized_by           UUID REFERENCES users(id),
    created_by              UUID NOT NULL REFERENCES users(id),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_change_sets_project_id ON change_sets(project_id);
CREATE INDEX IF NOT EXISTS idx_change_sets_status ON change_sets(status);

CREATE TABLE IF NOT EXISTS change_set_candidates (
    change_set_id   UUID NOT NULL REFERENCES change_sets(id) ON DELETE CASCADE,
    candidate_id    UUID NOT NULL UNIQUE REFERENCES change_candidates(id) ON DELETE CASCADE,
    added_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (change_set_id, candidate_id)
);

CREATE INDEX IF NOT EXISTS idx_change_set_candidates_candidate_id
    ON change_set_candidates(candidate_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_change_set_candidates_candidate_id;
DROP TABLE IF EXISTS change_set_candidates;
DROP INDEX IF EXISTS idx_change_sets_status;
DROP INDEX IF EXISTS idx_change_sets_project_id;
DROP TABLE IF EXISTS change_sets;
-- +goose StatementEnd
