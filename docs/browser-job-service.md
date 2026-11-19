# Browser verification job service — durable queue contract

## Current scope

This service enqueues **read-only** verification jobs; it does not execute or schedule browsers from the API. The standalone Playwright verifier lives at `apps/browser-worker`. Production execution remains **disabled** until a network-isolated worker and authenticated lease/completion channel are integrated.

Authenticated Dev Plane API routes:

- `POST /api/v1/organizations/{orgID}/browser-jobs`: owner/admin only; `202 Accepted` on first submission, `200 OK` for an identical idempotent submission and `409 Conflict` for a reused key with a different spec.
- `GET /api/v1/organizations/{orgID}/browser-jobs/{id}`: organization member can read status. A nonexistent or cross-tenant job is a 404.

The POST body accepts `idempotency_key`, `start_url`, `checks` (`text_visible` or `selector_visible`), `timeout_ms` (1–120 seconds) and optional `capture` booleans. It rejects unknown fields, cross-origin targets, non-HTTPS URLs, mutation actions and oversized request bodies. The caller cannot supply `tenant_id` or `run_id`; the API derives tenant identity from authenticated claims and stores a contract without the eventual worker run ID. The worker adapter must bind the persisted job ID to the browser runner's `run_id` before executing and validate again at execution time.

`BROWSER_ALLOWED_ORIGINS=https://app.example.com` is **server-owned** exact HTTPS origin configuration, separate from CORS `ALLOWED_ORIGINS`. An empty browser allowlist disables new browser job submissions. Set the same egress policy in the worker environment; browser target URLs and evidence can contain sensitive information.

## Database contract

`packages/db/migrations/025_create_browser_jobs.sql` adds tenant-scoped jobs with `(organization_id, idempotency_key)` uniqueness, immutable spec digests, a state machine and fenced leasing. The `packages/db` store exposes `SubmitBrowserJob`, `GetBrowserJob`, `ClaimBrowserJob`, `RenewBrowserJobLease`, `CompleteBrowserJob` and `ReconcileExpiredBrowserJobs`.

State transitions:

```
queued ──(single CAS claim)──> running ──(fenced completion)──> passed | failed
                                │
                                └──(expired lease)────────────> needs_review
```

Every worker completion is fenced by `claimed_by`, a monotonically incremented claim epoch and an unexpired lease. An expired job is marked `needs_review` and cannot be automatically reclaimed. A state transition is not proof that a page mutation did or did not happen. Recovery requires independent reconciliation before a fresh job can be authorized.

## Remaining production gates

1. Authenticated worker identities and credential-less, tenant-scoped job dispatch, with durable reconciliation after worker/host loss.
2. Isolated browser runtime with egress firewall, DNS/private-network protections, tenant-scoped profiles and CPU/memory/time ceilings. Origin allowlists alone are **not** an SSRF defense.
3. Encrypted and access-controlled artifact storage with retention and redaction. Screenshots and traces may include personal data and secrets.
4. Repeat the successful disposable PostgreSQL 18.6 migration/idempotency/lease integration proof in CI; add mixed-tenant/parallel claim tests and staged environment proof.
5. Explicit authority/receipt handling for any future action that can change external state. No click, form-submit or cross-origin writes are allowed in this version.

Do not expose `ClaimBrowserJob`, `RenewBrowserJobLease` or `CompleteBrowserJob` as user-facing endpoints. They are internal store primitives, not authenticated worker APIs.
