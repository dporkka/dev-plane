# Agent egress policy foundation (not yet active)

The Go API contains `internal/egresspolicy`: a small, dependency-free,
fail-closed policy model for a future network enforcement gateway. It borrows
the **control-plane/data-plane split** from Envoy and the **scoped consumers**
concept from APISIX. No proxy is installed by this change.

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

## Mandatory requirements before enforcement

This is a **policy foundation, not a live egress firewall or SSRF solution**.
Real agent isolation requires a separate enforced network boundary (isolated
namespaces, explicit proxying, or host firewall) so agents cannot connect
directly around the gateway.

1. Resolve requested names in the gateway and pin approved IPs in the actual
   dialer while retaining the requested hostname for TLS verification.
   Merely checking DNS before ordinary dialing is DNS-rebinding vulnerable.
2. Re-run the policy and IP checks on **every redirect**. Never forward provider
   credentials to a redirected host.
3. Deny other protocols, raw sockets, CONNECT tunnels, and arbitrary ports
   at the sandbox/network boundary. Reject outbound traffic when the gateway
   or control plane is unavailable, subject to a documented last-known-good
   policy TTL.
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
