-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS secret_references (
    id              UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    project_id      UUID REFERENCES projects(id),
    name            TEXT NOT NULL,
    scope           TEXT NOT NULL,
    provider        TEXT NOT NULL,
    key_path        TEXT NOT NULL,
    description     TEXT,
    last_rotated_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at      TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_secret_refs_org_project_name_scope
    ON secret_references(organization_id, COALESCE(project_id, '00000000-0000-0000-0000-000000000000'), name, scope)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_secret_refs_org ON secret_references(organization_id);
CREATE INDEX IF NOT EXISTS idx_secret_refs_project ON secret_references(project_id);
CREATE INDEX IF NOT EXISTS idx_secret_refs_scope ON secret_references(scope);
CREATE INDEX IF NOT EXISTS idx_secret_refs_deleted_at ON secret_references(deleted_at);

CREATE TABLE IF NOT EXISTS secret_values (
    id                  UUID PRIMARY KEY,
    secret_reference_id UUID NOT NULL REFERENCES secret_references(id) ON DELETE CASCADE,
    version             INTEGER NOT NULL,
    key_id              TEXT NOT NULL,
    ciphertext          TEXT NOT NULL,
    active              BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    rotated_at          TIMESTAMPTZ,
    UNIQUE(secret_reference_id, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_secret_values_active
    ON secret_values(secret_reference_id)
    WHERE active = true;
CREATE INDEX IF NOT EXISTS idx_secret_values_reference ON secret_values(secret_reference_id);
CREATE INDEX IF NOT EXISTS idx_secret_values_key_id ON secret_values(key_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_secret_values_key_id;
DROP INDEX IF EXISTS idx_secret_values_reference;
DROP INDEX IF EXISTS idx_secret_values_active;
DROP TABLE IF EXISTS secret_values;
DROP INDEX IF EXISTS idx_secret_refs_deleted_at;
DROP INDEX IF EXISTS idx_secret_refs_scope;
DROP INDEX IF EXISTS idx_secret_refs_project;
DROP INDEX IF EXISTS idx_secret_refs_org;
DROP INDEX IF EXISTS idx_secret_refs_org_project_name_scope;
DROP TABLE IF EXISTS secret_references;
-- +goose StatementEnd
