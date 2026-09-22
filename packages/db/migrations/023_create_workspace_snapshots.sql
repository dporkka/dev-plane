-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS workspace_snapshots (
    id                       UUID PRIMARY KEY,
    workspace_id             UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    git_commit               TEXT,
    vcs_change_id            TEXT,
    artifact_manifest_digest TEXT,
    artifact_version_digest  TEXT,
    description              TEXT,
    metadata                 JSONB DEFAULT '{}',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (
        git_commit IS NOT NULL
        OR artifact_version_digest IS NOT NULL
        OR artifact_manifest_digest IS NOT NULL
    )
);

CREATE INDEX IF NOT EXISTS idx_workspace_snapshots_workspace_id
    ON workspace_snapshots(workspace_id);
CREATE INDEX IF NOT EXISTS idx_workspace_snapshots_created_at
    ON workspace_snapshots(created_at);
CREATE INDEX IF NOT EXISTS idx_workspace_snapshots_git_commit
    ON workspace_snapshots(git_commit);
CREATE INDEX IF NOT EXISTS idx_workspace_snapshots_artifact_version
    ON workspace_snapshots(artifact_version_digest);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_workspace_snapshots_artifact_version;
DROP INDEX IF EXISTS idx_workspace_snapshots_git_commit;
DROP INDEX IF EXISTS idx_workspace_snapshots_created_at;
DROP INDEX IF EXISTS idx_workspace_snapshots_workspace_id;
DROP TABLE IF EXISTS workspace_snapshots;
-- +goose StatementEnd
