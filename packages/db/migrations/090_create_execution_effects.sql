-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS execution_effects (
    effect_id       TEXT PRIMARY KEY,
    run_id          TEXT NOT NULL,
    activation_id   TEXT NOT NULL,
    epoch           BIGINT NOT NULL,
    ordinal         INTEGER NOT NULL,
    operation       TEXT NOT NULL,
    resource        TEXT NOT NULL,
    revision        TEXT NOT NULL DEFAULT '',
    input_digest    TEXT NOT NULL,
    provider        TEXT,
    reference       TEXT,
    output_digest   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_execution_effects_run_id
    ON execution_effects(run_id);
CREATE INDEX IF NOT EXISTS idx_execution_effects_activation
    ON execution_effects(run_id, activation_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_execution_effects_activation;
DROP INDEX IF EXISTS idx_execution_effects_run_id;
DROP TABLE IF EXISTS execution_effects;
-- +goose StatementEnd
