-- +goose Up
-- +goose StatementBegin
-- Durable at-most-once reservation for host-broker deliveries. Reservations
-- persist even when the outbound request or file delivery fails.
CREATE TABLE IF NOT EXISTS agent_egress_receipts (
    tenant_id UUID NOT NULL REFERENCES organizations(id),
    agent_run_id UUID NOT NULL REFERENCES agent_runs(id),
    runtime_session_id TEXT NOT NULL,
    receipt_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (agent_run_id, receipt_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_egress_receipts_tenant_created
    ON agent_egress_receipts(tenant_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_agent_egress_receipts_tenant_created;
DROP TABLE IF EXISTS agent_egress_receipts;
-- +goose StatementEnd
