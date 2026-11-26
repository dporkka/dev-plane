-- +goose Up
-- +goose StatementBegin
CREATE TABLE gitea_ci_work_bindings (
    pull_request_id UUID PRIMARY KEY REFERENCES gitea_pr_identity_receipts(pull_request_id),
    work_item_id TEXT NOT NULL UNIQUE REFERENCES work_items(id),
    repository_identity TEXT NOT NULL,
    head_sha TEXT NOT NULL CHECK (LENGTH(head_sha)=40),
    base_sha TEXT NOT NULL CHECK (LENGTH(base_sha)=40 AND base_sha<>head_sha),
    required_gates TEXT NOT NULL,
    bound_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE gitea_ci_binding_rollback_guard (allowed INTEGER NOT NULL CHECK (allowed=1));
INSERT INTO gitea_ci_binding_rollback_guard SELECT CASE WHEN EXISTS
    (SELECT 1 FROM gitea_ci_work_bindings) THEN 0 ELSE 1 END;
DROP TABLE gitea_ci_binding_rollback_guard;
DROP TABLE gitea_ci_work_bindings;
-- +goose StatementEnd
