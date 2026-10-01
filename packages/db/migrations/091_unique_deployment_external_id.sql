-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS idx_deployments_provider_external_id
    ON deployments(provider, external_id)
    WHERE external_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_deployments_provider_external_id;
-- +goose StatementEnd
