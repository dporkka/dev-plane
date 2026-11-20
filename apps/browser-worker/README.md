# Dev Plane browser verification worker (MVP)

This is a **local, deterministic, read-only** Playwright worker. It verifies a page on an operator-approved origin with independent DOM assertions, and persists a bounded result and optional evidence. It is **not** a TinyFish replacement, an autonomous browser agent, or a production distributed queue.

## Setup

Requires Node 22+ and a Chromium binary. From `apps/browser-worker`:

```bash
npm ci
npm test
```

To run the real Chromium integration tests, set `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/path/to/chromium`; tests that require Chromium otherwise skip. On systems with limited `/tmp` quota, set `TMPDIR` to a private location with sufficient storage.

## Run

Create an operator-owned artifact root with access restricted to the worker identity, then prepare a job JSON file:

```json
{
  "version": 1,
  "tenant_id": "example-tenant",
  "run_id": "verify-001",
  "start_url": "https://app.example.com/health",
  "checks": [
    { "type": "text_visible", "value": "Healthy" },
    { "type": "selector_visible", "value": "#status" }
  ],
  "timeout_ms": 15000,
  "capture": { "screenshot": true, "trace": true }
}
```

```bash
BROWSER_ALLOWED_ORIGINS=https://app.example.com \
BROWSER_ARTIFACT_ROOT=/private/browser-artifacts \
PLAYWRIGHT_CHROMIUM_EXECUTABLE=/usr/bin/chromium \
node src/cli.mjs job.json
```

`BROWSER_ALLOWED_ORIGINS` is operator-supplied configuration, **not** derived from the job. It is a comma-delimited exact origin allowlist. The start URL and every HTTP request are checked against it; non-GET/HEAD requests, WebSocket connections, cross-origin requests and unexpected schemes are blocked. `BROWSER_ALLOW_HTTP_LOOPBACK=1` enables HTTP **only** for loopback development tests. No credentials or browser profiles are imported. Jobs contain assertions only: no click, fill, evaluate or network mutation actions.

CLI exit status: `0` verified, `1` verification failed, `2` invalid job or unable to claim. Results are in `ARTIFACT_ROOT/tenant_id/run_id/result.json`; optional evidence is `screenshot.png` and `trace.zip`. A second identical completed run returns the stored result; a conflicting job with the same run ID is rejected. If `claim.json` remains after interruption and there is no final result, retries **fail closed** and require operator reconciliation. This is **not automatic crash recovery**.

**Security limitations:** An origin allowlist is not a network firewall or defense against DNS rebinding. GET requests can also have side effects on noncompliant target applications; this worker cannot guarantee safety against misdesigned servers. Run the browser in a restricted network namespace/container with metadata endpoints and private networks denied, no host credential mounts, resource quotas and a per-tenant artifact root. Screenshots and Playwright traces can capture session secrets or PII and must be access-controlled, encrypted and retained for a bounded period. There is no remote API, tenant authentication, durable scheduler, R2 upload, cloud-provider adapter, autonomous AI fallback or external-effect reconciliation yet. Do not expose this worker as an internet-facing service.

## Next integration milestone

Attach the worker to Dev Plane via an authenticated, tenant-scoped job adapter and durable PostgreSQL job ledger; store artifact references in an access-controlled object store. Reuse `packages/execution` authority/receipt primitives before enabling browser actions with side effects. Nulang Cloud should be a separate, qualification-gated execution provider; Chromium requires a browser-capable Linux runtime rather than Wasmtime alone.
