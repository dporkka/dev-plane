# Device enrollment storage — additive, not yet exposed

This staged service is **not** a remote device gateway and registers no HTTP
routes. The API must authenticate its human principal and pass a `devices.Scope`
from verified JWT/session claims; never accept organization/user IDs from a
request body. `Registry.member` independently checks current user and org
membership in the database before changes.

## Lifecycle

1. Authenticated owner calls `BeginEnrollment(scope, ed25519PublicKey)`.
2. Control plane generates and persists a random 256-bit challenge linked to
   actor, organization and public key, expiring after 60 seconds.
3. Device signs the canonical, domain-separated `EnrollmentMessage(challenge)`
   with its Ed25519 private key locally. The secret never reaches the server.
4. Owner calls `CompleteEnrollment(scope, nonce, signature)` using the **same
   authenticated principal**. Server rechecks membership, retrieves the stored
   challenge (ignoring client-supplied scope, public key and expiry), validates
   signature and time, atomically marks nonce consumed and inserts key.
5. Owner revokes with `Revoke(scope, deviceID)`. Revocation is permanent for
   that public-key-derived ID; a fresh key is needed to reenroll. Admin-managed
   emergency revocation can be added behind an explicit role-specific API.
6. Trusted device gateway calls `ActiveKey(deviceID)` **on every operation** and
   rejects absent, revoked, orphaned or invalid keys. Connections must also
   be immediately closed when revocation propagates.

`device_enrollment_challenges` retains consumed/expired nonce rows so replay
claims are auditable; periodically delete expired rows under an explicit
retention policy without deleting `enrolled_devices` revocation records.

## Security and deployment gates

- Registration and revocation HTTP handlers, authenticated request binding,
  explicit rate limits/quotas and CSRF defenses are **not yet implemented**.
- Device-to-gateway challenge verification, persistent revocation fan-out,
  per-device session fencing, per-tool delegated grants and a secure outbound
  relay remain required. Existing `apps/device-agent/internal/deviceauth`
  only provides local cryptographic primitives.
- Durable job execution and per-operation receipt reconciliation must precede
  any remotely enabled mutation. Do not route a remote session through
  `runtimes.LocalProvider` or a host Docker socket.
- New schema is introduced in migration `025_device_enrollment.sql`; run it
  with the existing Goose migration process after a database snapshot and
  test it against production-equivalent PostgreSQL before release.
- Production authorization must enforce **principal × organization × device ×
  workspace × tool** and check *both* gateway and device-side grants.
- These package methods are not API authorization middleware. The authenticated
  API route integration is the next change and must undergo GitNexus impact
  analysis and independent security review before enabling.

## Local verification

```sh
cd apps/api/internal/devices
GO111MODULE=off go test -race -count=1 .
GO111MODULE=off go vet .
python3 ../../../../scripts/tests/test_device_enrollment_sqlite.py
```

The Python test checks SQLite migration compatibility and actual uniqueness,
nonce, organization, owner and revocation SQL predicates. It does not replace
PostgreSQL integration testing or an end-to-end API authentication test.
