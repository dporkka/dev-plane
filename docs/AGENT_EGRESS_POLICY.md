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
- Uses the existing runtime provider's `WriteFile` to place a JSON receipt in
  `.dev-plane/egress/<receipt-id>.json`, where bytes are base64-encoded.
  No caller-supplied destination path or HTTP headers are accepted.

The bridge has **not** been attached to any public API endpoint, automatic
tool call, or production scheduler; a concrete session resolver backed by the
workspace/run authorization database is still required.

A separate opt-in integration test uses the real Docker runtime provider with
a locally pre-pulled Docker-compatible image, a local Git fixture, and a local
TLS server. It asserts `--network none`, fails a direct outbound connection,
allows a host-side broker request with pinned DNS and TLS validation, reads the
result from the isolated workspace, and rejects an unallowlisted destination.

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

**Remaining production gates:** a concrete authenticated scheduler/session
resolver; filesystem-level prevention of writable-symlink redirection within
agent-controlled paths; per-run request IDs and replay protection; usage budgets;
durable audit attribution; concurrency and revocation-in-flight behavior; and
exact-head CI against an isolated worker. The bridge is not a replacement for
the container's `--network none` isolation.
