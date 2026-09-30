-- +goose Up
-- +goose StatementBegin
ALTER TABLE agent_runs ADD COLUMN state_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE agent_runs ADD COLUMN processed_event_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN processing_event_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN event_claimed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_agent_runs_state_version ON agent_runs(id, state_version);
CREATE INDEX IF NOT EXISTS idx_agent_runs_event_processing
    ON agent_runs(processing_event_version, event_claimed_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_runs_event_processing;
DROP INDEX IF EXISTS idx_agent_runs_state_version;
ALTER TABLE agent_runs DROP COLUMN event_claimed_at;
ALTER TABLE agent_runs DROP COLUMN processing_event_version;
ALTER TABLE agent_runs DROP COLUMN processed_event_version;
ALTER TABLE agent_runs DROP COLUMN state_version;
-- +goose StatementEnd
