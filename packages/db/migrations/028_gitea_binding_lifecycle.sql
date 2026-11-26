-- +goose Up
-- +goose StatementBegin
ALTER TABLE gitea_repository_bindings ADD COLUMN authorization_generation BIGINT NOT NULL DEFAULT 1 CHECK (authorization_generation > 0);
ALTER TABLE gitea_repository_bindings ADD COLUMN revoked_at TIMESTAMPTZ;
CREATE TABLE gitea_binding_lifecycle_events (
    repository_id UUID NOT NULL REFERENCES gitea_repository_bindings(repository_id),
    authorization_generation BIGINT NOT NULL CHECK (authorization_generation > 1),
    action TEXT NOT NULL CHECK (action IN ('revoke','reactivate')),
    reason TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (repository_id, authorization_generation)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Never silently erase revocation history or revive an old authorization.
CREATE TABLE gitea_binding_rollback_guard (allowed INTEGER NOT NULL CHECK (allowed = 1));
INSERT INTO gitea_binding_rollback_guard SELECT CASE WHEN EXISTS
    (SELECT 1 FROM gitea_binding_lifecycle_events) OR EXISTS
    (SELECT 1 FROM gitea_repository_bindings WHERE revoked_at IS NOT NULL OR authorization_generation <> 1)
    THEN 0 ELSE 1 END;
DROP TABLE gitea_binding_rollback_guard;
DROP TABLE gitea_binding_lifecycle_events;
ALTER TABLE gitea_repository_bindings DROP COLUMN revoked_at;
ALTER TABLE gitea_repository_bindings DROP COLUMN authorization_generation;
-- +goose StatementEnd
