-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS decision_packets (
    id              UUID PRIMARY KEY,
    candidate_id    UUID NOT NULL UNIQUE REFERENCES change_candidates(id) ON DELETE CASCADE,
    digest          TEXT NOT NULL,
    packet          JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_decision_packets_digest ON decision_packets(digest);
CREATE INDEX IF NOT EXISTS idx_decision_packets_created_at ON decision_packets(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_decision_packets_created_at;
DROP INDEX IF EXISTS idx_decision_packets_digest;
DROP TABLE IF EXISTS decision_packets;
-- +goose StatementEnd
