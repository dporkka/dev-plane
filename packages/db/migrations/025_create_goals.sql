-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS goals (
    id              TEXT PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    created_by      UUID NOT NULL REFERENCES users(id),
    status          TEXT NOT NULL,
    proof_epoch     TEXT NOT NULL,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_goals_organization_status ON goals(organization_id, status);
CREATE INDEX IF NOT EXISTS idx_goals_proof_epoch ON goals(proof_epoch);

CREATE TABLE IF NOT EXISTS goal_work_items (
    goal_id          TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    work_item_id     TEXT NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
    subject_revision TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (goal_id, work_item_id)
);

CREATE INDEX IF NOT EXISTS idx_goal_work_items_work_item_id ON goal_work_items(work_item_id);
CREATE INDEX IF NOT EXISTS idx_goal_work_items_subject_revision ON goal_work_items(subject_revision);

CREATE TABLE IF NOT EXISTS goal_proofs (
    goal_id          TEXT NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    criterion_id     TEXT NOT NULL,
    criterion_digest TEXT NOT NULL,
    evidence_id      TEXT NOT NULL,
    subject_revision TEXT NOT NULL,
    proof_epoch      TEXT NOT NULL,
    status           TEXT NOT NULL,
    observed_at      TIMESTAMPTZ NOT NULL,
    payload          JSONB NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (goal_id, criterion_id, evidence_id)
);

CREATE INDEX IF NOT EXISTS idx_goal_proofs_goal_epoch ON goal_proofs(goal_id, proof_epoch);
CREATE INDEX IF NOT EXISTS idx_goal_proofs_evidence_id ON goal_proofs(evidence_id);
CREATE INDEX IF NOT EXISTS idx_goal_proofs_subject_revision ON goal_proofs(subject_revision);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_goal_proofs_subject_revision;
DROP INDEX IF EXISTS idx_goal_proofs_evidence_id;
DROP INDEX IF EXISTS idx_goal_proofs_goal_epoch;
DROP TABLE IF EXISTS goal_proofs;
DROP INDEX IF EXISTS idx_goal_work_items_subject_revision;
DROP INDEX IF EXISTS idx_goal_work_items_work_item_id;
DROP TABLE IF EXISTS goal_work_items;
DROP INDEX IF EXISTS idx_goals_proof_epoch;
DROP INDEX IF EXISTS idx_goals_organization_status;
DROP TABLE IF EXISTS goals;
-- +goose StatementEnd
