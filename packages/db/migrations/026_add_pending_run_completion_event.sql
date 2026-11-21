-- +goose Up
ALTER TABLE agent_runs ADD COLUMN pending_completion_event TEXT;

-- +goose Down
ALTER TABLE agent_runs DROP COLUMN pending_completion_event;
