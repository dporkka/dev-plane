# Read-only Woodpecker diagnostics and atomic gate evidence

This change extends the signed remote CI contract merged in PR #198
(prerequisite #197 is also merged). It adds a **read-only provider adapter**,
not an automatic release approval.

## Woodpecker inspection

`verifier.WoodpeckerReader.ObserveAttempt` makes four authenticated GETs
to a configured Woodpecker server:

- `/api/repos/{repo_id}` to confirm the repository ID and full name;
- `/api/repos/{repo_id}/pipelines/{pipeline_number}` to confirm exact full
  source SHA, pipeline ID and number, rerun count, workflow/step and agent;
- `/api/repos/{repo_id}/pipelines/{pipeline_number}/config` to obtain the
  pipeline-scoped configuration bytes and calculate a deterministic digest;
- `/api/repos/{repo_id}/pipelines/{pipeline_number}` again to ensure the
  attempt, worker, step and pipeline state did not change during inspection.

The reader requires an explicit non-null `rerun_count` (zero is valid)
and validates full lowercase hexadecimal 40- or 64-character Git SHAs.
It rejects malformed or mismatched identifiers, missing/duplicate
configuration files, non-200 HTTP responses, unexpected redirects, absent
credentials, non-local plain HTTP, and oversized responses. A reported
successful step must include worker identity, valid start/finish timestamps,
and an observed zero exit code. Any change between the two pipeline snapshots
invalidates the diagnostic observation. These checks reduce false positives
but do not provide immutable provider attestation. The reader does not modify
the CI server or infer remote execution from forge statuses or webhook text.

**Blocking gap:** The documented pipeline response exposes workflow
`agent_id` and step `started`/`finished`, but does not prove the time
the worker was *assigned*. `FetchAttempt` therefore refuses to return a
signable `RemoteCIRecord` and returns
`ErrWoodpeckerAssignmentUnproven` after successfully observing a specific
attempt. This ensures `RemoteCIAttestor` cannot accidentally sign it as a
release-success record. The returned `WoodpeckerObservation` is diagnostic
and **must not** be interpreted as a release gate pass.

Additional rollout gates: source true assignment transition from a trusted
scheduler/event log, distinguish retry attempts (rather than just pipeline
rerun count), verify the executed pipeline-definition bytes and any claimed
artifact digests, restrict the attestor's private key, and exercise the
reader against an authenticated disposable Woodpecker instance. Those are
not proven by unit tests using a local HTTP fake.

## Atomic evidence merge

`db.MergeEvidenceGates` is a new append-only per-gate operation keyed by
`(work_item_id, head_sha)`. The base SHA must also match. The work-item,
head and base identifiers cannot be empty or contain surrounding whitespace,
so writes cannot disagree with normalized read lookups. A duplicate identical
gate is idempotent; a conflicting gate name or base is rejected without
modifying any stored evidence.

Before comparing existing and incoming gates, the merge normalizes the input
through the **same JSON representation** as persistence. This is necessary
because Go `time.Time` values lose monotonic clock fields during JSON
serialization, and `omitempty` converts non-nil empty artifact slices into
absent fields. Comparing in-memory structs against JSON-decoded stored values
would otherwise reject legitimate first inserts and idempotent retries.
Different persisted execution facts still conflict and fail closed.

- SQLite: `BEGIN IMMEDIATE` on a pinned connection serializes writers
  before a read/merge/write. Rollback on error.
- PostgreSQL: transactional `INSERT ... ON CONFLICT DO NOTHING` then
  `SELECT ... FOR UPDATE` locks the record before merging. Rollback on error.
- Multiple independent gates are preserved. No partial bundle is silently
  treated as a complete release approval.
- The prior `PutEvidenceBundle` replacement API is unchanged for existing
  callers. A future remote ingestion endpoint **must use**
  `MergeEvidenceGates`, not that replacement API, and must verify receipts
  against server-managed policies before persisting qualifying results.

Tests cover idempotency, exact persisted execution timestamps, empty
remote artifact slices, whitespace identifiers, duplicate/conflicting gates,
base conflicts, concurrent merges against one SQLite connection pool,
concurrent merges from two separate SQLite handles sharing a file, missing
rerun metadata, and malformed source SHAs. PostgreSQL integration and a real
Woodpecker instance are not tested in this PR.

## Security and release policy

This implementation installs no PATs or signing keys, changes no merge/release
approval settings, and does not grant an external CI provider or an agent
permission to sign its own release-success evidence. Keep the chained PRs
in draft until review and all actual CI gates are verified.
