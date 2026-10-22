# Device trust protocol (experimental; remote access NOT enabled)

This directory contains cryptographic building blocks for the **future** authenticated outbound device connection. It does not implement a public HTTP endpoint, relay, WebSocket client, enrollment API, or remote command executor. Only the existing local stdio MCP server runs.

## Local identity

On a Linux machine, build the identity helper from `apps/device-agent`:

```bash
GOWORK=off go build -o devplane-identity ./cmd/devplane-identity
./devplane-identity init --key "$HOME/.config/devplane/device.pem"
./devplane-identity show --key "$HOME/.config/devplane/device.pem"
```

`init` generates an Ed25519 PKCS#8 key in a new, owner-only (0600) file, refusing to overwrite existing keys or follow a final-component symlink. `show` prints only the deterministic device ID and URL-safe base64 public key; never upload the PEM/private key. Enrollment is **not** implemented, and the server does not consume this key yet.

## Planned enrollment and connection

1. An authenticated owner/admin calls a future Dev Plane API to enroll a specific public key with a specific organization and permitted workspace binding. Enrollment, revocation, and audit records must be durable.
2. The gateway sends a 32-byte cryptographic challenge, bound to the enrolled device ID, the `dev-plane/device-session/v1` audience and a 45-second lifetime. The device signs it with its local private key.
3. The gateway verifies against **the stored public key**, checks the pending nonce and expiry, and consumes the nonce to prevent replay. `deviceauth.Verifier` currently provides only an **in-memory reference implementation**; it is unsuitable as the system of record or multi-replica verifier.
4. After authenticating the requesting user and authorizing the device, workspace, tool, and operation through Dev Plane's existing authz/capability/approval layers, the gateway may sign a strictly scoped `deviceauth.Grant` using a **separate gateway signing key**.
5. The device pins that signing key and verifies the signature, audience, actor/organization identity, its own device ID, workspace ID, exact allowed tool, and the two-minute maximum lifetime **on each tool call**. The token cannot authorize arbitrary tools. Device revocation must invalidate any already-issued grant (e.g., via a revocation epoch checked by the agent); expiry alone is insufficient.
6. The device's **outbound-only** authenticated TLS connection carries authorized requests. No inbound laptop port is required. Disconnected and indeterminate executions must not be automatically retried.

## Trust boundaries and limitations

- **Authentication is not authorization.** Possession of a device key does not grant access to any workspace or tool.
- `Register` and `SignGrant` are library primitives for a trusted control plane. They MUST NOT be exposed as unauthenticated APIs.
- The package does not currently persist enrolled public keys, revocations, pending challenges, signed grants, or audit events.
- No transport or session router connects `deviceauth` to the MCP server yet. Do not advertise remote access as working.
- The existing Git profiles launch a host `git` executable and may process repository-controlled config. They MUST NOT be exposed to untrusted remote callers before process isolation, git configuration hardening, negative security tests and resource limits are qualified.
- All remote write, deployment, shell and secret operations remain disabled. An authorized gateway grant cannot bypass host-side OS isolation.
- Existing Dev Plane `packages/execution` operation IDs and receipts should own durable execution deduplication. Do not introduce a second scheduler or job authority.
- Use existing Dev Plane authenticated API, `authz`, capability kernel, approval service and runtime provider interfaces for the server integration.

## Security regression tests

```bash
cd apps/device-agent
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Tests cover private-key persistence and permissions, symlink protection, corrupted key rejection, unknown and revoked devices, invalid signatures, tampered audience/nonces, expiry, replay, exact workspace/tool matching, unknown-tool rejection, and gateway signing-key mismatch.
