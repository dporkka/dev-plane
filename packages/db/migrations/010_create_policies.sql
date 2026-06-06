-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS policies (
    id              UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    project_id      UUID REFERENCES projects(id),
    name            TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    action          TEXT NOT NULL,
    effect          TEXT NOT NULL,
    conditions      JSONB DEFAULT '{}',
    priority        INTEGER DEFAULT 100,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_policies_organization_id ON policies(organization_id);
CREATE INDEX IF NOT EXISTS idx_policies_project_id ON policies(project_id);
CREATE INDEX IF NOT EXISTS idx_policies_resource_type ON policies(resource_type);
CREATE INDEX IF NOT EXISTS idx_policies_effect ON policies(effect);
CREATE INDEX IF NOT EXISTS idx_policies_created_at ON policies(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_policies_created_at;
DROP INDEX IF EXISTS idx_policies_effect;
DROP INDEX IF EXISTS idx_policies_resource_type;
DROP INDEX IF EXISTS idx_policies_project_id;
DROP INDEX IF EXISTS idx_policies_organization_id;
DROP TABLE IF EXISTS policies;
-- +goose StatementEnd
