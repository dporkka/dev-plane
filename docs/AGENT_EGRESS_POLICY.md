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
