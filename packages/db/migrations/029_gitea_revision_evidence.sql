-- +goose Up
-- +goose StatementBegin
-- An attestation is also the single-use durable intent for a reviewed push.
-- No cascade deletes: evidence must survive ordinary application lifecycle changes.
CREATE TABLE gitea_revision_attestations (
    approval_id UUID PRIMARY KEY REFERENCES approvals(id),
    task_id UUID NOT NULL REFERENCES tasks(id),
    run_id UUID NOT NULL REFERENCES agent_runs(id),
    review_id UUID NOT NULL REFERENCES review_reports(id),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    repository_id UUID NOT NULL REFERENCES gitea_repository_bindings(repository_id),
    project_id UUID NOT NULL REFERENCES projects(id),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    approved_by UUID NOT NULL REFERENCES users(id),
    instance_url TEXT NOT NULL,
    upstream_repository_id BIGINT NOT NULL CHECK (upstream_repository_id > 0),
    authorization_generation BIGINT NOT NULL CHECK (authorization_generation > 0),
    branch TEXT NOT NULL,
    base_branch TEXT NOT NULL,
    base_sha TEXT NOT NULL CHECK (LENGTH(base_sha) = 40),
    head_sha TEXT NOT NULL CHECK (LENGTH(head_sha) = 40 AND head_sha <> base_sha),
    approval_responded_at TIMESTAMPTZ NOT NULL,
    review_created_at TIMESTAMPTZ NOT NULL,
    attested_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (repository_id, branch),
    UNIQUE (approval_id, head_sha)
);
CREATE TABLE gitea_publication_receipts (
    approval_id UUID PRIMARY KEY,
    observed_head_sha TEXT NOT NULL CHECK (LENGTH(observed_head_sha) = 40),
    confirmed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (approval_id, observed_head_sha)
      REFERENCES gitea_revision_attestations(approval_id, head_sha)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Refuse rollback if doing so would erase a consumed approval or receipt.
CREATE TABLE gitea_evidence_rollback_guard (allowed INTEGER NOT NULL CHECK (allowed = 1));
INSERT INTO gitea_evidence_rollback_guard SELECT CASE WHEN EXISTS
    (SELECT 1 FROM gitea_revision_attestations) OR EXISTS
    (SELECT 1 FROM gitea_publication_receipts)
    THEN 0 ELSE 1 END;
DROP TABLE gitea_evidence_rollback_guard;
DROP TABLE gitea_publication_receipts;
DROP TABLE gitea_revision_attestations;
-- +goose StatementEnd
