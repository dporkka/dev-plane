-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS verified_candidates (
    run_id                UUID PRIMARY KEY REFERENCES agent_runs(id) ON DELETE CASCADE,
    task_id               UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    workspace_id          UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    commit_id             TEXT NOT NULL,
    change_id             TEXT,
    source_branch         TEXT NOT NULL,
    target_branch         TEXT NOT NULL,
    integration_branch    TEXT,
    integrated_commit     TEXT,
    status                TEXT NOT NULL,
    initial_verification  JSONB NOT NULL DEFAULT '{}',
    final_verification    JSONB NOT NULL DEFAULT '{}',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_verified_candidates_task_id
    ON verified_candidates(task_id);
CREATE INDEX IF NOT EXISTS idx_verified_candidates_status
    ON verified_candidates(status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_verified_candidates_status;
DROP INDEX IF EXISTS idx_verified_candidates_task_id;
DROP TABLE IF EXISTS verified_candidates;
-- +goose StatementEnd
