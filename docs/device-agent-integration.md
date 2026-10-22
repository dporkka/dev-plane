# Device MCP integration — staged adoption

**Decision:** Dev Plane (`dporkka/dev-plane`) is the canonical software-development control plane. Keep the MCP device-side adapter here, rather than in Nulang Cloud. Nulang Cloud is a future optional execution provider; Adacavo's `devctl` is a workflow adapter, not another controller.

## Existing owner and reusable implementation

| Concern | Canonical implementation | Integration behavior |
| --- | --- | --- |
| Human authentication and organization scoping | `apps/api/internal/auth`, `apps/api/internal/authz` | Verify caller and the project's organization before issuing grants |
| Operation approvals and policy | `apps/api/internal/capability`, `apps/api/internal/approvals`, `packages/policies` | Persist approval, scope action to exact device/workspace/operation; fail closed |
| Run identity, fencing, receipts | `packages/execution` | Reuse activation epochs and operation identities, never invent a competing durable job authority |
| Workspaces, isolated commands and snapshots | `packages/runtimes.Provider` | Require Docker/microVM isolation for remote writes and testing; no host shell |
| Scheduling and execution | `packages/scheduler`, `apps/worker`, `apps/runner` | Continue existing task and run state machines |
| Task feedback and browser | `apps/api`, `apps/web` | Add devices/approvals views after pairing and receipts work |
| Git worktree PR pipeline | Adacavo `scripts/devctl.mjs` | Optional adapter; preserve exact-head verification and independent review |
| Cloud-hosted durable runtime | Nulang Cloud | Implement later behind runtime Provider contract, not through host-agent privileges |

## Secure remote architecture (not implemented by this PR)

1. The existing Dev Plane API authenticates the human and MCP client, authorizes organization and repository, and creates a server-side grant with an expiration and unique request ID.
2. A separately identified device initiates an **outbound-only** authenticated connection to a relay. No agent LAN service or internet-exposed socket.
3. For each request, the device verifies a signed grant bound to device ID, workspace ID, tool+arguments digest, actor, expiry, and revocation epoch. Connection presence alone never grants filesystem access.
4. The agent sends execution-status events to the canonical Dev Plane execution ledger; durable storage records issued/running/completed/failed/cancelled/**unknown-outcome** transitions and output cursors. On disconnect, reconcile instead of blindly retrying side effects.
5. Operations requiring mutation are executed through isolated provider sessions with explicit approvals, resource/network/secrets limits, redacted outputs, and release gates. Approval is never a substitute for a sandbox.

## Deliberate exclusions in first PR

- No remote transport, OAuth implementation, device-pairing UI, or credentials.
- No arbitrary command, file write, dependency installation, deploy, or git push.
- No reuse of Nulang Cloud's Firecracker `host-agent` as a workstation daemon.
- No new task tables or independent queue/scheduler; use Dev Plane's existing contracts.

## Merge gates and next PRs

1. Verify local Go tests/race/vet and stdio MCP smoke test on Fedora/Linux; review path, Git config and secret disclosure limitations.
2. Add API-managed device identities, signed short-lived grants, revocation and an outbound relay; test cross-org, replay, stale grant, and stolen-device cases before any remote tool calls.
3. Add canonical persisted job receipts and reconnect reconciliation using `packages/execution`; test duplicate request IDs, network partitions, concurrent owners, and cancellation.
4. Implement sandbox-backed `Provider` adapter and opt-in Adacavo `devctl` task integration; require human approval, exact-head review and PR-based changes.
5. After integration parity, close the superseded Nulang Cloud prototype PR #1116 to avoid divergent implementations.
