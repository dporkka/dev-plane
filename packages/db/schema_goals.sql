-- Proof-bound goals and goal/work-item revision bindings.
-- Kept as an additive schema fragment to avoid rewriting the legacy monolithic
-- schema.sql while preserving exact schema-vs-migrations drift verification.

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
