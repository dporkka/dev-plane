-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_steps (
    id              UUID PRIMARY KEY,
    agent_run_id    UUID NOT NULL REFERENCES agent_runs(id),
    step_number     INTEGER NOT NULL,
    step_type       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    content         TEXT,
    tool_name       TEXT,
    tool_input      JSONB,
    tool_output     JSONB,
    command         TEXT,
    command_output  TEXT,
    exit_code       INTEGER,
    file_path       TEXT,
    diff            TEXT,
    cost            DECIMAL(10,6) DEFAULT 0,
    latency_ms      INTEGER DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_steps_agent_run_id ON agent_steps(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_steps_step_number ON agent_steps(agent_run_id, step_number);
CREATE INDEX IF NOT EXISTS idx_agent_steps_status ON agent_steps(status);
CREATE INDEX IF NOT EXISTS idx_agent_steps_created_at ON agent_steps(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_steps_created_at;
DROP INDEX IF EXISTS idx_agent_steps_status;
DROP INDEX IF EXISTS idx_agent_steps_step_number;
DROP INDEX IF EXISTS idx_agent_steps_agent_run_id;
DROP TABLE IF EXISTS agent_steps;
-- +goose StatementEnd
