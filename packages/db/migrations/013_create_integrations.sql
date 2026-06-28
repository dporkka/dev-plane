-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS integrations (
    id                      UUID PRIMARY KEY,
    organization_id         UUID NOT NULL REFERENCES organizations(id),
    integration_type        TEXT NOT NULL,
    display_name            TEXT NOT NULL,
    config                  JSONB DEFAULT '{}',
    credentials_encrypted   TEXT,
    status                  TEXT NOT NULL DEFAULT 'pending',
    webhook_url             TEXT,
    last_synced_at          TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at              TIMESTAMPTZ,
    UNIQUE(organization_id, integration_type)
);

CREATE INDEX IF NOT EXISTS idx_integrations_organization_id ON integrations(organization_id);
CREATE INDEX IF NOT EXISTS idx_integrations_status ON integrations(status);
CREATE INDEX IF NOT EXISTS idx_integrations_deleted_at ON integrations(deleted_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_integrations_deleted_at;
DROP INDEX IF EXISTS idx_integrations_status;
DROP INDEX IF EXISTS idx_integrations_organization_id;
DROP TABLE IF EXISTS integrations;
-- +goose StatementEnd
