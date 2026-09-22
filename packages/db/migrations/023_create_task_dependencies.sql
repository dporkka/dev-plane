-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS task_dependencies (
    task_id             UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on_task_id  UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, depends_on_task_id),
    CHECK (task_id <> depends_on_task_id)
);

CREATE INDEX IF NOT EXISTS idx_task_dependencies_task_id
    ON task_dependencies(task_id);
CREATE INDEX IF NOT EXISTS idx_task_dependencies_depends_on_task_id
    ON task_dependencies(depends_on_task_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_task_dependencies_depends_on_task_id;
DROP INDEX IF EXISTS idx_task_dependencies_task_id;
DROP TABLE IF EXISTS task_dependencies;
-- +goose StatementEnd
