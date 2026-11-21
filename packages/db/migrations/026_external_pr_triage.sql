-- +goose Up
-- +goose StatementBegin
-- External PRs are distinct from agent-created pull_requests, which require a task.
-- This table only caches advisory snapshots; it never grants merge authority.
CREATE TABLE IF NOT EXISTS external_pr_triage (
    repository_id UUID NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    number INTEGER NOT NULL CHECK (number > 0),
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    head_sha TEXT NOT NULL,
    base_branch TEXT NOT NULL,
    bucket TEXT NOT NULL,
    score INTEGER NOT NULL,
    draft BOOLEAN NOT NULL,
    reasons TEXT NOT NULL DEFAULT '[]',
    dependencies TEXT NOT NULL DEFAULT '[]',
    snapshot_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (repository_id, number)
);
CREATE INDEX IF NOT EXISTS idx_external_pr_triage_org_priority
    ON external_pr_triage (organization_id, score DESC, snapshot_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS external_pr_triage;
-- +goose StatementEnd
