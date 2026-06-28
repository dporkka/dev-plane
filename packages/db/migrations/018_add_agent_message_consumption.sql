-- +goose Up
-- +goose StatementBegin
ALTER TABLE agent_messages ADD COLUMN consumed_at TIMESTAMPTZ;
ALTER TABLE agent_messages ADD COLUMN consumed_by_run_id UUID REFERENCES agent_runs(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_agent_messages_consumed_at ON agent_messages(consumed_at);
CREATE INDEX IF NOT EXISTS idx_agent_messages_consumed_by_run_id ON agent_messages(consumed_by_run_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_messages_consumed_by_run_id;
DROP INDEX IF EXISTS idx_agent_messages_consumed_at;
-- SQLite cannot drop columns portably; leave consumed_at and consumed_by_run_id in place on down migrations.
-- +goose StatementEnd
