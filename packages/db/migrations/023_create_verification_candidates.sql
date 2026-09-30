-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS change_candidates (
    id              UUID PRIMARY KEY,
    pull_request_id UUID NOT NULL UNIQUE REFERENCES pull_requests(id) ON DELETE CASCADE,
    task_id         UUID NOT NULL REFERENCES tasks(id),
    run_id          UUID NOT NULL REFERENCES agent_runs(id),
    workspace_id    UUID REFERENCES workspaces(id),
    repository_id   UUID NOT NULL REFERENCES repositories(id),
    commit_sha      TEXT NOT NULL,
    tree_hash       TEXT NOT NULL,
    branch          TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_change_candidates_task_id ON change_candidates(task_id);
CREATE INDEX IF NOT EXISTS idx_change_candidates_run_id ON change_candidates(run_id);
CREATE INDEX IF NOT EXISTS idx_change_candidates_repository_id ON change_candidates(repository_id);
CREATE INDEX IF NOT EXISTS idx_change_candidates_tree_hash ON change_candidates(tree_hash);

CREATE TABLE IF NOT EXISTS verification_evidence (
    id                  UUID PRIMARY KEY,
    candidate_id        UUID NOT NULL REFERENCES change_candidates(id) ON DELETE CASCADE,
    tree_hash           TEXT NOT NULL,
    contract_hash       TEXT NOT NULL,
    environment_digest  TEXT NOT NULL,
    runner_identity     TEXT NOT NULL,
    checks              JSONB NOT NULL DEFAULT '[]',
    started_at          TIMESTAMPTZ NOT NULL,
    completed_at        TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_verification_evidence_candidate_id ON verification_evidence(candidate_id);
CREATE INDEX IF NOT EXISTS idx_verification_evidence_tree_hash ON verification_evidence(tree_hash);
CREATE INDEX IF NOT EXISTS idx_verification_evidence_completed_at ON verification_evidence(completed_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_verification_evidence_completed_at;
DROP INDEX IF EXISTS idx_verification_evidence_tree_hash;
DROP INDEX IF EXISTS idx_verification_evidence_candidate_id;
DROP TABLE IF EXISTS verification_evidence;
DROP INDEX IF EXISTS idx_change_candidates_tree_hash;
DROP INDEX IF EXISTS idx_change_candidates_repository_id;
DROP INDEX IF EXISTS idx_change_candidates_run_id;
DROP INDEX IF EXISTS idx_change_candidates_task_id;
DROP TABLE IF EXISTS change_candidates;
-- +goose StatementEnd
