-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS forge_workload_requests (
    request_id        TEXT PRIMARY KEY,
    workload_id       TEXT NOT NULL,
    body_sha256       TEXT NOT NULL,
    task_id           UUID NOT NULL REFERENCES tasks(id),
    run_id            UUID NOT NULL REFERENCES agent_runs(id),
    operation         TEXT NOT NULL,
    state             TEXT NOT NULL DEFAULT 'pending',
    response_status   INTEGER,
    response_body     TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at      TIMESTAMPTZ,
    CHECK (state IN ('pending', 'completed', 'uncertain'))
);

CREATE INDEX IF NOT EXISTS idx_forge_workload_requests_task_id
    ON forge_workload_requests(task_id);
CREATE INDEX IF NOT EXISTS idx_forge_workload_requests_run_id
    ON forge_workload_requests(run_id);
CREATE INDEX IF NOT EXISTS idx_forge_workload_requests_created_at
    ON forge_workload_requests(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_forge_workload_requests_created_at;
DROP INDEX IF EXISTS idx_forge_workload_requests_run_id;
DROP INDEX IF EXISTS idx_forge_workload_requests_task_id;
DROP TABLE IF EXISTS forge_workload_requests;
-- +goose StatementEnd
