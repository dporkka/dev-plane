-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS reviewed_candidates (
    run_id                UUID PRIMARY KEY REFERENCES agent_runs(id) ON DELETE CASCADE,
    task_id               UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    workspace_id          UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    candidate_commit_id   TEXT NOT NULL,
    candidate_change_id   TEXT,
    candidate_branch      TEXT NOT NULL,
    candidate_changed     BOOLEAN NOT NULL DEFAULT false,
    candidate_agent_id    TEXT,
    review_digest         TEXT NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_reviewed_candidates_task_id
    ON reviewed_candidates(task_id);
CREATE INDEX IF NOT EXISTS idx_reviewed_candidates_workspace_id
    ON reviewed_candidates(workspace_id);
CREATE INDEX IF NOT EXISTS idx_reviewed_candidates_commit_id
    ON reviewed_candidates(candidate_commit_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_reviewed_candidates_commit_id;
DROP INDEX IF EXISTS idx_reviewed_candidates_workspace_id;
DROP INDEX IF EXISTS idx_reviewed_candidates_task_id;
DROP TABLE IF EXISTS reviewed_candidates;
-- +goose StatementEnd
