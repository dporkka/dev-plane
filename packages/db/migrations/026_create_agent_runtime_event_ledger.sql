-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_runtime_events (
    workspace_id TEXT NOT NULL,
    thread_id    TEXT NOT NULL REFERENCES agent_threads(id) ON DELETE CASCADE,
    turn_id      TEXT NOT NULL DEFAULT '',
    provider     TEXT NOT NULL,
    sequence     BIGINT NOT NULL CHECK (sequence > 0),
    event_type   TEXT NOT NULL,
    event_record JSONB NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    recorded_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (thread_id, turn_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_agent_runtime_events_workspace
    ON agent_runtime_events(workspace_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_agent_runtime_events_thread
    ON agent_runtime_events(thread_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_agent_runtime_events_provider
    ON agent_runtime_events(provider, occurred_at);
CREATE INDEX IF NOT EXISTS idx_agent_runtime_events_type
    ON agent_runtime_events(event_type, occurred_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_runtime_events_type;
DROP INDEX IF EXISTS idx_agent_runtime_events_provider;
DROP INDEX IF EXISTS idx_agent_runtime_events_thread;
DROP INDEX IF EXISTS idx_agent_runtime_events_workspace;
DROP TABLE IF EXISTS agent_runtime_events;
-- +goose StatementEnd
