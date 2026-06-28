-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS pull_requests (
    id              UUID PRIMARY KEY,
    task_id         UUID NOT NULL REFERENCES tasks(id),
    run_id          UUID REFERENCES agent_runs(id),
    repository_id   UUID NOT NULL REFERENCES repositories(id),
    number          INTEGER NOT NULL,
    title           TEXT NOT NULL,
    body            TEXT,
    branch          TEXT NOT NULL,
    base_branch     TEXT NOT NULL,
    url             TEXT NOT NULL,
    state           TEXT NOT NULL DEFAULT 'open',
    draft           BOOLEAN NOT NULL DEFAULT false,
    created_by      UUID NOT NULL REFERENCES users(id),
    merged_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repository_id, number)
);

CREATE INDEX IF NOT EXISTS idx_pull_requests_task_id ON pull_requests(task_id);
CREATE INDEX IF NOT EXISTS idx_pull_requests_repository_id ON pull_requests(repository_id);
CREATE INDEX IF NOT EXISTS idx_pull_requests_state ON pull_requests(state);
CREATE INDEX IF NOT EXISTS idx_pull_requests_created_at ON pull_requests(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_pull_requests_created_at;
DROP INDEX IF EXISTS idx_pull_requests_state;
DROP INDEX IF EXISTS idx_pull_requests_repository_id;
DROP INDEX IF EXISTS idx_pull_requests_task_id;
DROP TABLE IF EXISTS pull_requests;
-- +goose StatementEnd
