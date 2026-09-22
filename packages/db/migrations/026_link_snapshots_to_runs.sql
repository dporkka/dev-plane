-- +goose Up
-- +goose StatementBegin
ALTER TABLE workspace_snapshots
    ADD COLUMN agent_run_id UUID REFERENCES agent_runs(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_snapshots_agent_run_id
    ON workspace_snapshots(agent_run_id)
    WHERE agent_run_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_workspace_snapshots_agent_run_id;
-- SQLite cannot drop columns without rebuilding the table; keep the nullable
-- column on rollback and remove only the uniqueness/indexing constraint.
-- +goose StatementEnd
