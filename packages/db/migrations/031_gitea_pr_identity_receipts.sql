-- +goose Up
-- +goose StatementBegin
CREATE TABLE gitea_pr_identity_receipts (
    pull_request_id UUID PRIMARY KEY REFERENCES pull_requests(id),
    approval_id UUID NOT NULL UNIQUE REFERENCES gitea_pr_creation_intents(approval_id),
    instance_url TEXT NOT NULL,
    upstream_repository_id BIGINT NOT NULL CHECK (upstream_repository_id > 0),
    upstream_pr_id BIGINT NOT NULL CHECK (upstream_pr_id > 0),
    upstream_pr_number INTEGER NOT NULL CHECK (upstream_pr_number > 0),
    head_sha TEXT NOT NULL CHECK (LENGTH(head_sha) = 40),
    base_sha TEXT NOT NULL CHECK (LENGTH(base_sha) = 40 AND base_sha <> head_sha),
    confirmed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (instance_url, upstream_pr_id),
    FOREIGN KEY (approval_id, head_sha) REFERENCES gitea_revision_attestations(approval_id, head_sha)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE gitea_pr_identity_rollback_guard (allowed INTEGER NOT NULL CHECK (allowed = 1));
INSERT INTO gitea_pr_identity_rollback_guard SELECT CASE WHEN EXISTS
    (SELECT 1 FROM gitea_pr_identity_receipts) THEN 0 ELSE 1 END;
DROP TABLE gitea_pr_identity_rollback_guard;
DROP TABLE gitea_pr_identity_receipts;
-- +goose StatementEnd
