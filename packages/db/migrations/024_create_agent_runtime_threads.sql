-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_threads (
    id                 TEXT PRIMARY KEY,
    workspace_id       TEXT NOT NULL,
    provider           TEXT NOT NULL,
    provider_thread_id TEXT,
    model              TEXT,
    status             TEXT NOT NULL,
    payload            JSONB NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_threads_provider_native
    ON agent_threads(provider, provider_thread_id)
    WHERE provider_thread_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_threads_workspace_id ON agent_threads(workspace_id);
CREATE INDEX IF NOT EXISTS idx_agent_threads_status ON agent_threads(status);

CREATE TABLE IF NOT EXISTS agent_turns (
    id               TEXT PRIMARY KEY,
    thread_id        TEXT NOT NULL REFERENCES agent_threads(id) ON DELETE CASCADE,
    provider_turn_id TEXT,
    status           TEXT NOT NULL,
    payload          JSONB NOT NULL,
    started_at       TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_turns_provider_native
    ON agent_turns(thread_id, provider_turn_id)
    WHERE provider_turn_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_turns_thread_id ON agent_turns(thread_id);
CREATE INDEX IF NOT EXISTS idx_agent_turns_status ON agent_turns(status);

CREATE TABLE IF NOT EXISTS agent_items (
    id               TEXT PRIMARY KEY,
    thread_id        TEXT NOT NULL REFERENCES agent_threads(id) ON DELETE CASCADE,
    turn_id          TEXT NOT NULL REFERENCES agent_turns(id) ON DELETE CASCADE,
    provider_item_id TEXT,
    item_type        TEXT NOT NULL,
    status           TEXT NOT NULL,
    payload          JSONB NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_items_provider_native
    ON agent_items(turn_id, provider_item_id)
    WHERE provider_item_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_items_thread_id ON agent_items(thread_id);
CREATE INDEX IF NOT EXISTS idx_agent_items_turn_id ON agent_items(turn_id);
CREATE INDEX IF NOT EXISTS idx_agent_items_type ON agent_items(item_type);
CREATE INDEX IF NOT EXISTS idx_agent_items_status ON agent_items(status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_items_status;
DROP INDEX IF EXISTS idx_agent_items_type;
DROP INDEX IF EXISTS idx_agent_items_turn_id;
DROP INDEX IF EXISTS idx_agent_items_thread_id;
DROP INDEX IF EXISTS idx_agent_items_provider_native;
DROP TABLE IF EXISTS agent_items;

DROP INDEX IF EXISTS idx_agent_turns_status;
DROP INDEX IF EXISTS idx_agent_turns_thread_id;
DROP INDEX IF EXISTS idx_agent_turns_provider_native;
DROP TABLE IF EXISTS agent_turns;

DROP INDEX IF EXISTS idx_agent_threads_status;
DROP INDEX IF EXISTS idx_agent_threads_workspace_id;
DROP INDEX IF EXISTS idx_agent_threads_provider_native;
DROP TABLE IF EXISTS agent_threads;
-- +goose StatementEnd
