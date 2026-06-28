-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS model_usage (
    id                  UUID PRIMARY KEY,
    agent_run_id        UUID REFERENCES agent_runs(id),
    task_id             UUID NOT NULL REFERENCES tasks(id),
    model               TEXT NOT NULL,
    provider            TEXT NOT NULL,
    prompt_tokens       INTEGER NOT NULL DEFAULT 0,
    completion_tokens   INTEGER NOT NULL DEFAULT 0,
    total_tokens        INTEGER NOT NULL DEFAULT 0,
    cost                DECIMAL(10,6) NOT NULL DEFAULT 0,
    latency_ms          INTEGER DEFAULT 0,
    success             BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_model_usage_agent_run_id ON model_usage(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_model_usage_task_id ON model_usage(task_id);
CREATE INDEX IF NOT EXISTS idx_model_usage_model ON model_usage(model);
CREATE INDEX IF NOT EXISTS idx_model_usage_created_at ON model_usage(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_model_usage_created_at;
DROP INDEX IF EXISTS idx_model_usage_model;
DROP INDEX IF EXISTS idx_model_usage_task_id;
DROP INDEX IF EXISTS idx_model_usage_agent_run_id;
DROP TABLE IF EXISTS model_usage;
-- +goose StatementEnd
