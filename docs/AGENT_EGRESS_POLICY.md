# Agent egress policy foundation (not yet active)

The Go API contains `internal/egresspolicy`: a small, dependency-free,
fail-closed policy model and a bounded host-side HTTPS request broker. It borrows
the **control-plane/data-plane split** from Envoy and the **scoped consumers**
concept from APISIX. The broker is not exposed as a public proxy or wired into an agent execution path.

## Existing behavior versus proposed enforcement

- `Store.Apply` atomically admits complete, monotonic policy revisions.
- A rule binds one **trusted tenant/agent identity** to exact, lowercase HTTPS
  hostnames. Wildcards, IP literals, duplicated scopes, and malformed names
  are rejected. An empty valid snapshot revokes existing policies.
- `Authorize` refuses unknown scopes and hosts, non-HTTPS requests, nonstandard
  ports, credentials in URLs, and missing or nonpublic DNS answers.
- Denied and malformed snapshots do not replace the last approved snapshot.
- Policy mutations copy their inputs. The caller cannot change active rules
  by editing a previously applied Go slice.
- These rules **cannot be selected by a user-controlled HTTP header**.
  The agent identity must be derived from the authenticated scheduler context.

## Bounded host-side request broker (implemented, not activated)

`NewBroker(store).Fetch(ctx, trustedTenantID, trustedAgentID, url)`
performs a bounded HTTPS GET from the trusted host process. It:
- Resolves each destination and rejects an entire DNS answer set if any IP is
  private, reserved, metadata, NAT64, or an IPv4-tunneling address.
- Pins the approved DNS addresses into the actual TCP dialer; TLS verification
  continues to use the original hostname and system trust roots.
- Disables environment proxy inheritance, compression, connection reuse, and
  caller-supplied credential forwarding.
- Revalidates identity, URL, policy, and DNS before **each redirect**, with at
  most three redirects.
- Enforces per-fetch timeouts and a default 1 MiB response-size cap.
- Does not allow raw CONNECT, arbitrary methods, or user-provided headers.

Focused TLS, redirect, DNS-rebinding, and size-cap tests pass under the
Go race detector. The broker intentionally has **no HTTP listener**; callers
must integrate it into an authenticated scheduler API.

## Mandatory requirements before production enforcement

This is a **policy foundation, not a live egress firewall or SSRF solution**.
Real agent isolation requires a separate enforced network boundary (isolated
namespaces, explicit proxying, or host firewall) so agents cannot connect
directly around the gateway.

1. Connect the broker to an authenticated host-side worker or gateway API
   and explicitly authorize which workload can invoke each tenant/agent scope.
2. Continue enforcing Docker `--network none` until a separate, audited
   network namespace / proxy-only path is proven to prevent direct egress,
   raw sockets, CONNECT tunnels, and arbitrary-port bypass.
3. Require broker availability; document a bounded fail-closed policy TTL and
   verify policy-revision revocation behavior.
4. Derive identity from signed workload credentials, not `x-tenant-id`,
   `x-agent-id`, or other caller-supplied strings.
5. Add budget/concurrency enforcement **separately** from host allowlists:
   this version does not meter token usage or provide persistent quotas.
6. Observe policy rejections by revision and workload ID, but avoid putting
   authentication secrets, prompts, or URL query strings into logs.

## Verification

`cd apps/api && go test -race ./internal/egresspolicy`

Initial rollout should remain opt-in behind an isolated staging agent,
with forbidden metadata, private network, DNS rebinding, redirect, and
concurrent-policy-replacement tests. There is no production traffic cutover
in this change.


## Isolated runtime bridge and live Docker/Podman test (draft)

`WorkspaceBridge` is a narrow host-only adapter that:
- Calls a **trusted `SessionResolver`** to authorize the requesting principal
  (from its context) and bind a runtime session to a tenant and agent.
- Refuses a missing resolver, missing session binding, invalid receipt IDs,
  unapproved hosts, and failed upstream responses before writing any data.
- Calls the bounded `Broker.Fetch` only with the trusted resolved identity.
- Requires a dedicated `WriteEgressReceipt` writer. Generic workspace
  `WriteFile` is incompatible with the bridge and cannot be used as a
  fallback.
- Publishes JSON receipts under `/run/dev-plane/egress/<receipt-id>.json`
  inside a read-only bind mount of a private per-session host-owned directory;
  receipt payload bytes remain base64-encoded.
- Does not accept a caller-supplied output path or request headers.

The bridge has **not** been attached to any public API endpoint, automatic
tool call, or production scheduler; a concrete session resolver backed by the
workspace/run authorization database is still required.

A separate opt-in integration test uses the real Docker runtime provider with
a locally pre-pulled Docker-compatible image, a local Git fixture, and a local
TLS server. It asserts `--network none`, fails a direct outbound connection,
allows a host-side broker request with pinned DNS and TLS validation, reads the
result from the readonly-mounted artifact directory, verifies that hostile
workspace symlinks cannot redirect it, checks that the sandbox cannot overwrite
or create files on the mount, and rejects an unallowlisted destination.

Run with a pre-pulled image that contains `sh`, `git`, and `curl`:

```bash
cd apps/api
EGRESS_DOCKER_TEST_IMAGE=docker.io/library/node:24-bookworm \
RUN_DOCKER_EGRESS_INTEGRATION=1 \
go test ./internal/egresspolicy -run '^TestDockerEgressBrokerIsolationIntegration$' -count=1 -v
```

The test is skipped by default and fails (rather than silently skipping) when
explicitly enabled but Docker, Git, or the test image is missing. The test does
not require any real external network connection.

**Remaining production gates:** worker-service authentication and verified
scheduler authorization, per-run request/byte budgets, audited and expiring
storage, final authorization/revocation race handling, remote runner support,
and exact-head CI against supported isolation backends. The bridge is not a
replacement for the container's `--network none` isolation.

## Database-backed session authorization (draft, not activated)

A concrete `NewDatabaseSessionResolver(db)` now implements the bridge's
`SessionResolver` for a **user-authenticated, trusted-run-scoped** request.
The caller must supply a validated user identity via the existing JWT
middleware and select a run in trusted server code using
`WithAuthorizedRun(ctx, runID)`. The run ID is **not** an authorization
credential on its own.

The resolver performs a fresh database join of `workspaces`, `agent_runs`,
`tasks`, `repositories`, `projects`, and the current `users` row. It
requires a running agent run associated with exactly the requested runtime
session and task, an active Docker workspace, matching project/repository
ownership, no soft-deleted resources, and authenticated organization
membership. A non-admin/non-owner user must have created the task. The
returned broker `AgentID` is the exact run UUID, not the agent role or a
caller-provided arbitrary header; policy snapshots must provision that scope.

This *does not* register a public route or authorize background service
identity automatically. Background worker access requires a separate trusted
workload authentication contract rather than fabricating user claims.
Auditable user/agent identity must be resolved again before delivery to
prevent writing a response for a revoked or completed run.

`session_resolver_test.go` covers successful binding, missing claims,
missing run scope, tenant/run mismatch, absent active runs and database
failures. Focused tests passed after using a worktree-local Go telemetry
configuration with mode `off`; the first runs hit a Go telemetry mmap
SIGBUS under exhausted temporary-space quotas.

The `WorkspaceBridge` now **requires** a `ReceiptStore` and refuses all
requests if it is missing. The provided `SQLReceiptStore` uses migration
`packages/db/migrations/025_create_agent_egress_receipts.sql` and a
database unique constraint on `(agent_run_id, receipt_id)` to reserve
a receipt **before** external I/O. Duplicate deliveries are rejected
across processes; failed requests retain their reservations, and retries
must use new IDs. A table migration is required **before** enabling the
bridge; a missing table causes fail-closed denial.

After fetching, the bridge again checks authenticated session ownership
and the active policy revision. A revoked run or changed snapshot prevents
delivering a previously fetched response. It cannot undo upstream I/O that
occurred before revocation, nor provide a fully atomic revocation/delivery
boundary when a policy changes between the final check and file write.

**Readonly artifact delivery remediation (stacked draft PR):** The
bridge now requires an `EgressReceiptWriter`, implemented locally by
`DockerProvider.WriteEgressReceipt`, and never invokes the general
workspace write method. The Docker runtime creates a host-owned 0700
per-session directory and binds it read-only into the sandbox using `:ro,Z`
to support SELinux-confined Podman as well as Docker. Every receipt write
checks Docker inspect for the exact expected read-only mount and fails
closed if the container lacks it (including older running containers).

The writer validates the session and receipt IDs, caps payload size, and
creates a random 0000-permission staging inode inside the SELinux-labeled
host directory. It writes and synchronizes all data, then uses a
create-exclusive hardlink into the final name and promotes the complete
file to mode 0600. The agent's mount remains read-only, so it cannot
modify the temporary file, hardlink, final receipt, or mount directory.
Existing receipt names, including symlinks, cannot be overwritten.
Destroyed sessions remove the host-owned staging and artifact data.

A real Fedora rootless Podman integration test with a local TLS fixture
verifies direct egress denial, approved broker delivery, read-only mount
enforcement, prevention of writable-workspace symlink redirection, and
absence of denied receipts. The runtime's unit tests cover absent/writable
mounts, host symlinks, malicious preexisting receipt symlinks, duplicate
IDs and invalid paths.

**Remaining release blockers:** This works only when the broker and the
Docker runtime share the same trusted host; no secure remote-runner receipt
transport is implemented. A policy change after the final authorization
check but before publication remains a race. Per-run artifact limits,
periodic retention, durable audit events, workload identity and
staging/production exact-head CI qualification remain outstanding.
No public endpoint or automatic agent tool is enabled.
Verification:
- `go test -race ./internal/egresspolicy ./internal/modelrouter -count=1`
- `go vet ./internal/egresspolicy`
- Opt-in live test: `RUN_DOCKER_EGRESS_INTEGRATION=1` with a pre-pulled
  `EGRESS_DOCKER_TEST_IMAGE` as documented above.