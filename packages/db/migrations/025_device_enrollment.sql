-- +goose Up
-- +goose StatementBegin
-- Revocation is terminal for a device ID (derived from Ed25519 public key).
CREATE TABLE IF NOT EXISTS enrolled_devices (
    device_id TEXT PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    owner_user_id UUID NOT NULL REFERENCES users(id),
    public_key BYTEA NOT NULL,
    enrolled_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT enrolled_device_public_key_len CHECK (length(public_key) = 32)
);
CREATE INDEX IF NOT EXISTS idx_enrolled_devices_owner ON enrolled_devices(organization_id, owner_user_id);
CREATE INDEX IF NOT EXISTS idx_enrolled_devices_revoked ON enrolled_devices(revoked_at);

CREATE TABLE IF NOT EXISTS device_enrollment_challenges (
    nonce TEXT PRIMARY KEY,
    device_id TEXT NOT NULL,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    user_id UUID NOT NULL REFERENCES users(id),
    public_key BYTEA NOT NULL,
    issued_unix BIGINT NOT NULL,
    expires_unix BIGINT NOT NULL,
    consumed_unix BIGINT,
    CONSTRAINT enrollment_challenge_ttl CHECK (expires_unix > issued_unix AND expires_unix - issued_unix <= 120),
    CONSTRAINT enrollment_key_len CHECK (length(public_key) = 32)
);
CREATE INDEX IF NOT EXISTS idx_device_enrollment_scope ON device_enrollment_challenges(organization_id, user_id, nonce);
CREATE INDEX IF NOT EXISTS idx_device_enrollment_expiry ON device_enrollment_challenges(expires_unix);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_device_enrollment_expiry;
DROP INDEX IF EXISTS idx_device_enrollment_scope;
DROP TABLE IF EXISTS device_enrollment_challenges;
DROP INDEX IF EXISTS idx_enrolled_devices_revoked;
DROP INDEX IF EXISTS idx_enrolled_devices_owner;
DROP TABLE IF EXISTS enrolled_devices;
-- +goose StatementEnd
