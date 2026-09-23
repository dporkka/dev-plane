-- +goose Up
-- +goose StatementBegin
ALTER TABLE repositories ADD COLUMN IF NOT EXISTS forge_provider TEXT NOT NULL DEFAULT 'github';
ALTER TABLE repositories ADD COLUMN IF NOT EXISTS forge_base_url TEXT NOT NULL DEFAULT 'https://github.com';
ALTER TABLE repositories ADD COLUMN IF NOT EXISTS forge_repository_id TEXT;
ALTER TABLE repositories ADD COLUMN IF NOT EXISTS vcs_backend TEXT NOT NULL DEFAULT 'git';

UPDATE repositories
SET forge_repository_id = github_id::text
WHERE forge_repository_id IS NULL AND github_id IS NOT NULL;

ALTER TABLE repositories DROP CONSTRAINT IF EXISTS repositories_project_id_full_name_key;
ALTER TABLE repositories DROP CONSTRAINT IF EXISTS repositories_project_forge_full_name_key;
ALTER TABLE repositories
    ADD CONSTRAINT repositories_project_forge_full_name_key
    UNIQUE (project_id, forge_provider, forge_base_url, full_name);

CREATE INDEX IF NOT EXISTS idx_repositories_forge_provider ON repositories(forge_provider);
CREATE INDEX IF NOT EXISTS idx_repositories_vcs_backend ON repositories(vcs_backend);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_repositories_vcs_backend;
DROP INDEX IF EXISTS idx_repositories_forge_provider;
ALTER TABLE repositories DROP CONSTRAINT IF EXISTS repositories_project_forge_full_name_key;
ALTER TABLE repositories ADD CONSTRAINT repositories_project_id_full_name_key UNIQUE (project_id, full_name);
ALTER TABLE repositories DROP COLUMN IF EXISTS vcs_backend;
ALTER TABLE repositories DROP COLUMN IF EXISTS forge_repository_id;
ALTER TABLE repositories DROP COLUMN IF EXISTS forge_base_url;
ALTER TABLE repositories DROP COLUMN IF EXISTS forge_provider;
-- +goose StatementEnd
