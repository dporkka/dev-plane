-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS deployments (
    id              UUID PRIMARY KEY,
    task_id         UUID NOT NULL REFERENCES tasks(id),
    environment     TEXT NOT NULL,
    ref             TEXT NOT NULL,
    provider        TEXT NOT NULL DEFAULT 'github',
    external_id     TEXT,
    status          TEXT NOT NULL DEFAULT 'pending',
    url             TEXT,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_deployments_task_id ON deployments(task_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_deployments_task_id;
DROP TABLE IF EXISTS deployments;
-- +goose StatementEnd
