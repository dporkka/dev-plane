-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS budgets (
    id                      UUID PRIMARY KEY,
    organization_id         UUID NOT NULL REFERENCES organizations(id),
    project_id              UUID REFERENCES projects(id),
    task_id                 UUID REFERENCES tasks(id),
    type                    TEXT NOT NULL,
    period                  TEXT NOT NULL,
    max_cost                DECIMAL(10,4),
    max_runtime_minutes     INTEGER DEFAULT 0,
    max_model_calls         INTEGER DEFAULT 0,
    max_tool_calls          INTEGER DEFAULT 0,
    max_shell_commands      INTEGER DEFAULT 0,
    max_concurrent_agents   INTEGER DEFAULT 0,
    max_daily_spend         DECIMAL(10,4),
    notifications           JSONB DEFAULT '{}',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_budgets_organization_id ON budgets(organization_id);
CREATE INDEX IF NOT EXISTS idx_budgets_project_id ON budgets(project_id);
CREATE INDEX IF NOT EXISTS idx_budgets_task_id ON budgets(task_id);
CREATE INDEX IF NOT EXISTS idx_budgets_type ON budgets(type);
CREATE INDEX IF NOT EXISTS idx_budgets_created_at ON budgets(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_budgets_created_at;
DROP INDEX IF EXISTS idx_budgets_type;
DROP INDEX IF EXISTS idx_budgets_task_id;
DROP INDEX IF EXISTS idx_budgets_project_id;
DROP INDEX IF EXISTS idx_budgets_organization_id;
DROP TABLE IF EXISTS budgets;
-- +goose StatementEnd
