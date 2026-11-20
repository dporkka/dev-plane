# Remote CI qualification: PostgreSQL and Woodpecker

This validation-only change is stacked on PR #199 (itself stacked on #198
and #197). It does **not** activate remote CI as a trusted merge gate.

## PostgreSQL: real concurrent transactions

The opt-in test `TestPostgresEvidenceMergeRealConcurrentTransactions` requires
`DEVPLANE_TEST_POSTGRES_URL` pointing to a disposable database named exactly
`devplane_ciqual`. It creates only the `evidence_bundles` table from the
production migration shape. It does **not** use, truncate, or clean up an
existing application database. Each run uses unique work-item identifiers.

It verifies:

- 36 simultaneous independent gate submissions through two separately pooled
  PostgreSQL connections, all retained under one work-item/full head;
- an identical retry remains idempotent; a changed status or base SHA
  conflicts, rolls back, and leaves prior evidence intact;
- another update succeeds immediately after the rolled-back transaction;
- a distinct candidate head has independent evidence.

A local run on Fedora used PostgreSQL **18.6**, initialized in a private
temporary directory using a Unix socket with TCP listening disabled. The
race-detector validation **passed three consecutive runs** with Go's `-race`.
`make test` without `DEVPLANE_TEST_POSTGRES_URL` skips this live-DB test.

The integration schema is intentionally small and mirrors the
`evidence_bundles` table, not a proof of end-to-end production migrations.

## Woodpecker: API compatibility and authentication

The public Woodpecker API documents:

- `GET /api/repos/{repo_id}/pipelines/{pipeline_number}`
- `GET /api/repos/{repo_id}/pipelines/{pipeline_number}/config`
- pipeline `workflows[].agent_id`, `children[].started` and `finished`
- configuration `data` as a JSON array of octets

Regression tests now cover the documented numeric octet representation,
invalid bytes, missing bytes, and stable digest calculation across file order.

During this review, read-only health/version probes identified running
Woodpecker services on two connected hosts. The vmi3235364 instance
reported version 3.19.0. Both rejected anonymous repository and pipeline
reads with HTTP **401**. No credential was extracted or installed.
**This does not constitute an authenticated live pipeline qualification.**

`TestWoodpeckerRealReadOnlyAttempt` provides a guarded opt-in contract test
against an authorized server. It requires all of the following environment
variables, and refuses partial inputs:

- `DEVPLANE_WP_TEST_URL`, `DEVPLANE_WP_TEST_TOKEN`
- `DEVPLANE_WP_TEST_REPOSITORY`, `DEVPLANE_WP_TEST_REPO_ID`
- `DEVPLANE_WP_TEST_PIPELINE_ID`, `DEVPLANE_WP_TEST_PIPELINE_NUMBER`
- `DEVPLANE_WP_TEST_STEP_ID`, `DEVPLANE_WP_TEST_RERUN`
- `DEVPLANE_WP_TEST_HEAD_SHA`

Use a **read-only Woodpecker token**, supply one actual exact job attempt,
and run `go test ./packages/verifier -run TestWoodpeckerRealReadOnlyAttempt -v`.
Do not commit or print the token. The test performs GET requests only and
still requires `FetchAttempt` to reject issuance of release-signable
evidence because an immutable assignment timestamp is unproven.

## Remaining protected-release blockers

Woodpecker's authenticated pipeline API must be qualified with an exact
successful and unsuccessful job and a queued/abandoned job on a disposable
repo. Separately obtain tamper-evident assignment events and prove the exact
job retry and executed config. Ensure attached artifacts are hashed from
their bytes before attesting. Then verify receipt persistence/merge with a
trusted policy. Until all are proven, leave release-gate wiring and the
stacked pull requests in draft.
