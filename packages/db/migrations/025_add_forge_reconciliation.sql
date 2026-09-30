-- +goose Up
-- +goose StatementBegin
ALTER TABLE forge_workload_requests ADD COLUMN evidence JSONB;
ALTER TABLE forge_workload_requests ADD COLUMN lease_released BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE forge_workload_requests ADD COLUMN reconciled_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE forge_workload_requests DROP COLUMN reconciled_at;
ALTER TABLE forge_workload_requests DROP COLUMN lease_released;
ALTER TABLE forge_workload_requests DROP COLUMN evidence;
-- +goose StatementEnd
