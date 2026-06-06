-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS review_reports (
    id              UUID PRIMARY KEY,
    run_id          UUID NOT NULL UNIQUE REFERENCES agent_runs(id) ON DELETE CASCADE,
    summary         TEXT NOT NULL,
    findings        JSONB DEFAULT '[]',
    risk_level      TEXT NOT NULL,
    approvable      BOOLEAN NOT NULL DEFAULT false,
    suggestions     JSONB DEFAULT '[]',
    test_coverage   TEXT NOT NULL DEFAULT '',
    security_notes  TEXT NOT NULL DEFAULT '',
    diff_summary    JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_review_reports_run_id ON review_reports(run_id);
CREATE INDEX IF NOT EXISTS idx_review_reports_risk_level ON review_reports(risk_level);
CREATE INDEX IF NOT EXISTS idx_review_reports_created_at ON review_reports(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_review_reports_created_at;
DROP INDEX IF EXISTS idx_review_reports_risk_level;
DROP INDEX IF EXISTS idx_review_reports_run_id;
DROP TABLE IF EXISTS review_reports;
-- +goose StatementEnd
