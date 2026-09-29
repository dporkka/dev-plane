-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_project_source_source_id_active
    ON tasks(project_id, source, source_id)
    WHERE source_id IS NOT NULL AND deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_tasks_project_source_source_id_active;
-- +goose StatementEnd
