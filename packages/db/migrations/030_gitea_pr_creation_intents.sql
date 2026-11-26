-- +goose Up
-- +goose StatementBegin
-- One external creation attempt per confirmed publication and task.
CREATE TABLE gitea_pr_creation_intents (
    approval_id UUID PRIMARY KEY REFERENCES gitea_publication_receipts(approval_id),
    task_id UUID NOT NULL UNIQUE REFERENCES tasks(id),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE gitea_pr_intent_rollback_guard (allowed INTEGER NOT NULL CHECK (allowed = 1));
INSERT INTO gitea_pr_intent_rollback_guard SELECT CASE WHEN EXISTS
    (SELECT 1 FROM gitea_pr_creation_intents) THEN 0 ELSE 1 END;
DROP TABLE gitea_pr_intent_rollback_guard;
DROP TABLE gitea_pr_creation_intents;
-- +goose StatementEnd
