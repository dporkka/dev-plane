-- +goose Up
-- +goose StatementBegin
ALTER TABLE agent_runs ADD COLUMN parent_run_id UUID REFERENCES agent_runs(id);
ALTER TABLE agent_runs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 1;
ALTER TABLE agent_runs ADD COLUMN outcome TEXT;
ALTER TABLE agent_runs ADD COLUMN execution_snapshot JSONB NOT NULL DEFAULT '{}';
ALTER TABLE agent_runs ADD COLUMN execution_snapshot_digest TEXT;

CREATE INDEX IF NOT EXISTS idx_agent_runs_parent_run_id ON agent_runs(parent_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_runs_outcome ON agent_runs(outcome);
CREATE INDEX IF NOT EXISTS idx_agent_runs_task_attempt ON agent_runs(task_id, attempt);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_runs_parent_attempt_role
    ON agent_runs(parent_run_id, attempt, agent_role)
    WHERE parent_run_id IS NOT NULL;

ALTER TABLE agent_steps ADD COLUMN outcome TEXT;
ALTER TABLE agent_steps ADD COLUMN input JSONB;
ALTER TABLE agent_steps ADD COLUMN output JSONB;
ALTER TABLE agent_steps ADD COLUMN started_at TIMESTAMPTZ;
ALTER TABLE agent_steps ADD COLUMN completed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_agent_steps_outcome ON agent_steps(outcome);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_steps_outcome;
ALTER TABLE agent_steps DROP COLUMN completed_at;
ALTER TABLE agent_steps DROP COLUMN started_at;
ALTER TABLE agent_steps DROP COLUMN output;
ALTER TABLE agent_steps DROP COLUMN input;
ALTER TABLE agent_steps DROP COLUMN outcome;

DROP INDEX IF EXISTS idx_agent_runs_parent_attempt_role;
DROP INDEX IF EXISTS idx_agent_runs_task_attempt;
DROP INDEX IF EXISTS idx_agent_runs_outcome;
DROP INDEX IF EXISTS idx_agent_runs_parent_run_id;
ALTER TABLE agent_runs DROP COLUMN execution_snapshot_digest;
ALTER TABLE agent_runs DROP COLUMN execution_snapshot;
ALTER TABLE agent_runs DROP COLUMN outcome;
ALTER TABLE agent_runs DROP COLUMN attempt;
ALTER TABLE agent_runs DROP COLUMN parent_run_id;
-- +goose StatementEnd
