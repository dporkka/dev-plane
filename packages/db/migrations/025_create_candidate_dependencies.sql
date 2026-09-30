-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS candidate_dependencies (
    candidate_id            UUID NOT NULL REFERENCES change_candidates(id) ON DELETE CASCADE,
    depends_on_candidate_id UUID NOT NULL REFERENCES change_candidates(id) ON DELETE CASCADE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (candidate_id, depends_on_candidate_id),
    CHECK (candidate_id <> depends_on_candidate_id)
);

CREATE INDEX IF NOT EXISTS idx_candidate_dependencies_dependency
    ON candidate_dependencies(depends_on_candidate_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_candidate_dependencies_dependency;
DROP TABLE IF EXISTS candidate_dependencies;
-- +goose StatementEnd
