-- +goose Up
-- GitHub IDs are immutable identity keys. Refuse deployment if historical rows
-- disagree on ownership: duplicate provider subjects must be reconciled by
-- an administrator, never silently assigned to one organization.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_github_id_unique
    ON users(github_id) WHERE github_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_users_github_id_unique;
