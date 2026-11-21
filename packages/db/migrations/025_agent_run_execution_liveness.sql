-- +goose Up
-- +goose StatementBegin
-- A lease records *observable worker liveness*; expiry does not authorize replay.
-- The runner may have performed external side effects before losing its lease.
ALTER TABLE agent_runs ADD COLUMN execution_lease_token TEXT;
ALTER TABLE agent_runs ADD COLUMN execution_lease_expires_at TIMESTAMPTZ;
ALTER TABLE agent_runs ADD COLUMN execution_heartbeat_at TIMESTAMPTZ;
ALTER TABLE agent_runs ADD COLUMN recovery_review_required_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_agent_runs_liveness_review
  ON agent_runs (status, execution_lease_expires_at)
  WHERE execution_lease_token IS NOT NULL AND recovery_review_required_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_runs_liveness_review;
ALTER TABLE agent_runs DROP COLUMN recovery_review_required_at;
ALTER TABLE agent_runs DROP COLUMN execution_heartbeat_at;
ALTER TABLE agent_runs DROP COLUMN execution_lease_expires_at;
ALTER TABLE agent_runs DROP COLUMN execution_lease_token;
-- +goose StatementEnd
