# Dry-run promotion evaluation

This package evaluates exact-candidate promotion *eligibility*. It does not access Git remotes, publish checks, enqueue merges, deploy artifacts, or grant promotion authority.

## Trust boundaries

- Only trusted CI/verifier processes should create `VerificationAttempt` records.
- The trusted caller must fetch the latest source and destination refs, compute the actual candidate commit and tree, and provide these as `Snapshot`.
- `Policy` must come from a protected, pinned revision. Do **not** use a candidate-controlled `devplane.yaml`.
- Human approval records must come from authenticated, independently verified identities.
- `Eligible=true` is transient. A real promotion controller must recheck remote refs, follow the provider's protected branch/merge-queue semantics, verify the exact integration candidate and reconcile the resulting commit.

This is additive to the legacy `EvidenceBundle` and direct manual PR merging; **neither of those paths is connected to this decision engine** yet. Do not enable autonomous merging until that integration and failure-injection coverage are complete.

## Tests

```sh
cd packages/repoprotocol && go test ./...
cd ../promotion && go test ./...
```

Required regression cases include stale source, stale target, policy changes, unexpected candidate tree, missing and skipped checks, and insufficient independent approvals.

## GitHub observation (read-only)

The `github.Reader` adapter uses authenticated GET-only requests. It reads:

- The PR head and base SHAs and the current protected target branch;
- GitHub's test-merge commit/tree, requiring exactly two parents matching the current target and source;
- `devplane.yaml` from the *immutable protected target commit*, never the candidate branch;
- Review events for the current source revision, counting unique, current human approvals excluding the PR author.

The observer derives a SHA-256 policy digest and conservatively requires **all configured verification profiles** plus at least one independently verified human approval. Inaccessible refs, unprotected branches, stale merge commits, missing policy, and unavailable reviews fail closed.

### Authenticated API

```http
GET /api/v1/pull-requests/{internal_pr_id}/promotion-readiness?attempt_id={attempt_id}
Authorization: Bearer <application-session-token>
```

The caller must be authorized to access that pull request. The `attempt_id` must identify a concluded `verification_attempts` row for **the same repository and task**; arbitrary cross-task evidence is rejected. The response returns `decision.eligible`, `decision.dry_run`, `decision.blockers`, observed Git SHAs, and the trusted policy digest.

No mutation endpoint has been added. The legacy manual merge endpoint is unchanged and **does not consume** this assessment.

### Limitations before promotion authority

- Only ordinary GitHub test-merge commits with a verified two-parent identity are understood. Merge queues, squash, rebase and stacked integration require separate verified candidate semantics.
- The observer's remote reads are non-atomic. A positive assessment can become stale immediately and must never itself authorize a merge.
- Approval events are current-head GitHub reviews, **not** proof of every possible external branch-protection or organizational review rule.
- The API reads concluded attempts; connecting a trusted verifier to produce them is future work. Until valid attempts exist, the endpoint fails closed with `missing_attempt`.
- The exact production trust chain still needs signed runner attestations, persisted trusted identities, protected policy management, and privileged promotion reconciliation.

## Authenticated verification attempts (stacked draft #176)

A separate internal-only `verifier.VerifyTrustedCandidate` path runs mandatory gates from
**trusted protected policy bytes** against a clean checkout at the exact two-parent
integration candidate commit and tree. It checks `git rev-parse HEAD`, `HEAD^{tree}`,
merge parents, and non-ignored tracked/untracked changes before and after each gate.
Both success and test failure can be recorded as signed append-only evidence;
failures never become a promotion pass.

**Important:** The trusted worker (not an agent or API caller) must provision the
protected policy bytes, merge identities, disposable sandbox, runner digest and
signing key. Git status cannot prove filesystem or network hermeticity,
including ignored files, container mounts and remote dependencies. This path
is not yet invoked by the existing agent worker and does not silently upgrade
legacy evidence.

The read-only readiness API now refuses even structurally valid evidence if it
cannot verify its HMAC-SHA256 signature. Set
`PROMOTION_ATTESTATION_KEY_BASE64` to a securely generated base64-encoded
secret with at least 32 decoded bytes, distributed only to the trusted
verification worker and read-only assessment service. **No fallback key exists.**
Keep it inaccessible to coding agents, test processes, logs, and task payloads.
Key rotation/versioning and separation into asymmetric signing/verification keys
are intentionally not yet provided.

Database migration initialization now installs SQLite and PostgreSQL triggers
that reject `UPDATE` and `DELETE` against `verification_attempts`.
Production still needs separate schema-migration and application roles:
owners, superusers, or DDL-capable actors can remove triggers. Do not give
arbitrary SQL or DDL access to coding agents. Run migrations through a
privileged migration identity before starting a restricted runtime role.

**No merge endpoint is wired to signed evidence.** The only new operation is
read-only eligibility evaluation. Do not enable automated promotion until the
trusted worker is integrated end-to-end, independent code review is complete,
GitHub base and candidate identities are rechecked at merge time, and crash
reconciliation and live disposable-repo tests pass.
