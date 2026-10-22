-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS routing_decisions (
    id                          UUID PRIMARY KEY,
    agent_run_id                UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    task_id                     UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    step_number                 INTEGER NOT NULL,
    route                       TEXT NOT NULL,
    route_source                TEXT NOT NULL,
    policy_version              TEXT NOT NULL,
    task_type                   TEXT NOT NULL,
    difficulty                  TEXT NOT NULL,
    agent_role                  TEXT NOT NULL,
    risk_level                  TEXT NOT NULL,
    model                       TEXT NOT NULL,
    provider                    TEXT NOT NULL,
    spend_authority             TEXT NOT NULL,
    prompt_tokens               INTEGER NOT NULL DEFAULT 0,
    completion_tokens           INTEGER NOT NULL DEFAULT 0,
    total_tokens                INTEGER NOT NULL DEFAULT 0,
    estimated_cost              DECIMAL(10,6) NOT NULL DEFAULT 0,
    latency_ms                  INTEGER NOT NULL DEFAULT 0,
    provider_call_succeeded     BOOLEAN NOT NULL DEFAULT true,
    outcome_status              TEXT NOT NULL DEFAULT 'pending',
    verifier_passed             BOOLEAN,
    verifier_results            JSONB DEFAULT '{}',
    human_intervention_required BOOLEAN NOT NULL DEFAULT false,
    error                       TEXT,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    outcome_recorded_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_routing_decisions_agent_run_id ON routing_decisions(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_routing_decisions_task_id ON routing_decisions(task_id);
CREATE INDEX IF NOT EXISTS idx_routing_decisions_route ON routing_decisions(route);
CREATE INDEX IF NOT EXISTS idx_routing_decisions_model ON routing_decisions(model);
CREATE INDEX IF NOT EXISTS idx_routing_decisions_outcome_status ON routing_decisions(outcome_status);
CREATE INDEX IF NOT EXISTS idx_routing_decisions_created_at ON routing_decisions(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_routing_decisions_created_at;
DROP INDEX IF EXISTS idx_routing_decisions_outcome_status;
DROP INDEX IF EXISTS idx_routing_decisions_model;
DROP INDEX IF EXISTS idx_routing_decisions_route;
DROP INDEX IF EXISTS idx_routing_decisions_task_id;
DROP INDEX IF EXISTS idx_routing_decisions_agent_run_id;
DROP TABLE IF EXISTS routing_decisions;
-- +goose StatementEnd
