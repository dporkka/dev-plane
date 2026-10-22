# Durable device operation receipts (staged)

This is an additive, **server-internal** operation ledger. It depends on the device enrollment implementation and migration 025 from Dev Plane PR #191; this follow-up PR is based on that branch. No public HTTP routes, relays, workstation listeners, shell commands, or execution privileges are introduced.

## Architecture

- Canonical logical IDs come from `packages/execution.NewOperationID`. This component validates the `dp1_<sha256>` representation but does not define a second execution identity or queue.
- Dev Plane's existing authentication, capability kernel, approvals and workspace authorization must explicitly permit actor × organization × device × workspace × tool × exact argument bytes. The repository SQL independently enforces active device ownership and same-organization workspace membership at reservation and dispatch time.
- `ReserveOperation` inserts an immutable scoped intent. Repeated identical requests return the old record, whereas conflicting tool, digest, scope, or epoch fails closed.
- `DispatchOperation` is a single SQL compare-and-swap from `prepared` to `dispatched`, committed BEFORE sending across the future outbound relay. A crash after the CAS produces uncertainty, **not permission to retry**.
- Lost connections transition `dispatched → unknown`. Reconnects must reconcile; they cannot redispatch. An externally verified device receipt may resolve `dispatched|unknown → completed|failed`. Terminal records retain a SHA-256 digest, not raw output or secrets.
- Revoked devices cannot newly reserve or dispatch, and stale owner epochs cannot update an operation.

## Explicit release blockers

1. `RecordOperationReceipt` assumes the trusted gateway has already verified the device's Ed25519-signed response, grant signature, operation ID, argument digest, epoch and active/revoked status. **Never expose this function directly as a public RPC.**
2. These functions do not authenticate a human or enforce HTTP-level capability checks. Bind scopes to validated session claims, never client JSON.
3. No real relay, per-device outbound session, signature verifier, restart reattachment, isolated command provider, or encrypted artifact sink is present.
4. The unit tests and SQLite contract tests are valuable but not PostgreSQL validation. Run a temporary PostgreSQL migration/concurrency test and an independent security review before production rollout.
5. This intentionally stores only the ledger; `packages/execution` remains responsible for canonical operation identity, effect receipts, fencing and takeovers.

## Verification

From the repository root:

```sh
cd apps/api/internal/devices
GO111MODULE=off go test -race ./...
GO111MODULE=off go vet ./...
cd ../../../..
python3 scripts/tests/test_device_operation_sqlite.py
```

The next safe engineering step is an authenticated device-gateway adapter that checks current user/device authorization and signed receipt proofs. Remote general-purpose host shell access remains disabled.
