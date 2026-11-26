-- +goose Up
-- +goose StatementBegin
-- External instance receipts only. The repository ID is the Gitea numeric ID,
-- not a local FK or proof of authority over any Dev Plane project.
CREATE TABLE IF NOT EXISTS gitea_webhook_receipts (
    instance_url         TEXT NOT NULL,
    delivery_id          TEXT NOT NULL,
    payload_sha256       TEXT NOT NULL,
    repository_id        BIGINT NOT NULL,
    repository_full_name TEXT NOT NULL,
    event_type           TEXT NOT NULL,
    received_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (instance_url, delivery_id),
    UNIQUE (instance_url, payload_sha256)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS gitea_webhook_receipts;
-- +goose StatementEnd
