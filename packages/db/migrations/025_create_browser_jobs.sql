-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS browser_jobs (
    id              UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    idempotency_key TEXT NOT NULL,
    spec_digest     TEXT NOT NULL,
    payload         JSONB NOT NULL,
    state           TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','passed','failed','needs_review')),
    claimed_by      TEXT,
    claim_epoch     BIGINT NOT NULL DEFAULT 0,
    lease_until     TIMESTAMPTZ,
    result          JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (organization_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_browser_jobs_tenant_state ON browser_jobs(organization_id, state);
CREATE INDEX IF NOT EXISTS idx_browser_jobs_state_lease ON browser_jobs(state, lease_until);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_browser_jobs_state_lease;
DROP INDEX IF EXISTS idx_browser_jobs_tenant_state;
DROP TABLE IF EXISTS browser_jobs;
-- +goose StatementEnd
