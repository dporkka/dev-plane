-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS task_evaluations (
    id                      UUID PRIMARY KEY,
    task_id                 UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_run_id            UUID REFERENCES agent_runs(id) ON DELETE SET NULL,
    attempt                 INTEGER NOT NULL,
    accepted                BOOLEAN NOT NULL DEFAULT false,
    first_pass              BOOLEAN NOT NULL DEFAULT false,
    human_interventions     INTEGER NOT NULL DEFAULT 0,
    human_attention_seconds INTEGER NOT NULL DEFAULT 0,
    wall_clock_seconds      INTEGER NOT NULL DEFAULT 0,
    agent_compute_seconds   INTEGER NOT NULL DEFAULT 0,
    total_tokens            INTEGER NOT NULL DEFAULT 0,
    total_cost              DECIMAL(12,6) NOT NULL DEFAULT 0,
    tests_passed            INTEGER NOT NULL DEFAULT 0,
    tests_failed            INTEGER NOT NULL DEFAULT 0,
    review_findings         INTEGER NOT NULL DEFAULT 0,
    human_change_lines      INTEGER NOT NULL DEFAULT 0,
    reverted_within_7d      BOOLEAN NOT NULL DEFAULT false,
    production_regression   BOOLEAN NOT NULL DEFAULT false,
    model                   TEXT,
    provider                TEXT,
    prompt_version          TEXT,
    skill_version           TEXT,
    strategy                TEXT,
    metadata                JSONB NOT NULL DEFAULT '{}',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(task_id, attempt)
);

CREATE INDEX IF NOT EXISTS idx_task_evaluations_task_id ON task_evaluations(task_id);
CREATE INDEX IF NOT EXISTS idx_task_evaluations_agent_run_id ON task_evaluations(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_task_evaluations_created_at ON task_evaluations(created_at);
CREATE INDEX IF NOT EXISTS idx_task_evaluations_model_provider ON task_evaluations(model, provider);
CREATE INDEX IF NOT EXISTS idx_task_evaluations_strategy ON task_evaluations(strategy);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_task_evaluations_strategy;
DROP INDEX IF EXISTS idx_task_evaluations_model_provider;
DROP INDEX IF EXISTS idx_task_evaluations_created_at;
DROP INDEX IF EXISTS idx_task_evaluations_agent_run_id;
DROP INDEX IF EXISTS idx_task_evaluations_task_id;
DROP TABLE IF EXISTS task_evaluations;
-- +goose StatementEnd
