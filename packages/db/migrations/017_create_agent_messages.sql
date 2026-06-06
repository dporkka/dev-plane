-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_messages (
    id              UUID PRIMARY KEY,
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_run_id    UUID REFERENCES agent_runs(id) ON DELETE SET NULL,
    from_agent      TEXT NOT NULL,
    to_agent        TEXT NOT NULL,
    message_type    TEXT NOT NULL,
    content         TEXT NOT NULL,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_messages_task_id ON agent_messages(task_id);
CREATE INDEX IF NOT EXISTS idx_agent_messages_run_id ON agent_messages(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_messages_to_agent ON agent_messages(to_agent);
CREATE INDEX IF NOT EXISTS idx_agent_messages_type ON agent_messages(message_type);
CREATE INDEX IF NOT EXISTS idx_agent_messages_created_at ON agent_messages(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_messages_created_at;
DROP INDEX IF EXISTS idx_agent_messages_type;
DROP INDEX IF EXISTS idx_agent_messages_to_agent;
DROP INDEX IF EXISTS idx_agent_messages_run_id;
DROP INDEX IF EXISTS idx_agent_messages_task_id;
DROP TABLE IF EXISTS agent_messages;
-- +goose StatementEnd
