-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS approvals (
    id              UUID PRIMARY KEY,
    task_id         UUID NOT NULL REFERENCES tasks(id),
    agent_run_id    UUID REFERENCES agent_runs(id),
    approval_type   TEXT NOT NULL,
    requested_by    UUID NOT NULL REFERENCES users(id),
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    responded_by    UUID REFERENCES users(id),
    response        TEXT,
    response_note   TEXT,
    responded_at    TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_approvals_task_id ON approvals(task_id);
CREATE INDEX IF NOT EXISTS idx_approvals_agent_run_id ON approvals(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_approvals_requested_by ON approvals(requested_by);
CREATE INDEX IF NOT EXISTS idx_approvals_response ON approvals(response);
CREATE INDEX IF NOT EXISTS idx_approvals_created_at ON approvals(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_approvals_created_at;
DROP INDEX IF EXISTS idx_approvals_response;
DROP INDEX IF EXISTS idx_approvals_requested_by;
DROP INDEX IF EXISTS idx_approvals_agent_run_id;
DROP INDEX IF EXISTS idx_approvals_task_id;
DROP TABLE IF EXISTS approvals;
-- +goose StatementEnd
