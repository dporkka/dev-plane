# Remote CI execution evidence (v1)

This contract builds on the local structured gate evidence introduced in PR #197.
It is deliberately **fail-closed** and **not yet a live Woodpecker/Buildkite
integration**. No production secrets, external CI accounts, or release gates
are modified by this change.

## Ownership and trust boundary

- `packages/repoprotocol.RemoteCIRecord` normalizes observable CI pipeline
  identifiers, exact source commit, immutable pipeline definition SHA-256,
  run/job/attempt IDs, worker assignment, lifecycle timestamps, terminal
  outcome, exit code, and optional content-addressed artifacts.
- `packages/verifier.RemoteCIAttestor` requires an `AuthoritativeCIReader`
  capable of retrieving one exact job attempt from an authenticated provider
  API. A webhook or GitHub status is **not** an authoritative record.
- A privileged adapter signs the verified facts using Ed25519. The private key
  belongs outside job containers and agent workspaces; public keys and allowed
  provider/repository/pipeline-definition digests must be pinned by trusted
  server-side configuration.
- `EvidenceBundle.ValidateHead` continues rejecting remote-only gate proofs.
  `ValidateHeadWithRemoteCI` accepts them only when a trusted per-gate policy
  is explicitly passed. It verifies the attestation signature and release
  semantics; normal local gates use their existing runtime proof validation.

A valid signature establishes that the holder of the pinned attestation key
signed those facts. **It does not prove the adapter actually queried a CI API.**
Only a reviewed and independently tested provider adapter, restricted
credentials, and an auditable evidence source can establish that trust.

## Release eligibility

A remote gate qualifies only if all of the following hold:

1. Repository and full Git source SHA exactly match the approved candidate.
2. Provider identity and pipeline definition hash match the pinned policy.
3. Pipeline, run, job, and retry-attempt identities are present.
4. A nonempty worker ID is tied to an assignment, start, and finish in
   nondecreasing time order. No synthetic timestamp substitutions are allowed.
5. The job reached a terminal `passed` result with an actual exit code of 0.
   Queued, assigned, running, canceled, test-failed, and infrastructure-failed
   observations cannot qualify.
6. Every declared artifact includes a unique safe relative name, SHA-256 digest,
   and nonnegative size; no artifact is required unless separately demanded by
   the gate's approved policy.
7. The complete receipt verifies against a trusted Ed25519 public key.
   Any change to signed job IDs, worker data, exit status, times, or artifact
   hashes breaks verification.

External CI never satisfies independent review or human approval merely by
claiming a passed job. Signed receipts cannot impersonate those gates.

## Adapter rollout (next implementation gate)

For Woodpecker, build a read-only authenticated API adapter that retrieves
the exact pipeline by repository ID and pipeline number, then correlates
workflow and step data with authoritative agent assignment. The public API
provides pipeline commit/status/timing, workflow agent IDs, step start/finish,
and exit codes; **some required facts (notably assignment time and pipeline
definition digest) may not be directly available in the same response**.
Never invent them from workflow start time or current repository contents.

Before enabling the adapter in release policy:

- Resolve pipeline configuration bytes for the executed revision and hash
  those exact bytes rather than a mutable branch configuration.
- Obtain actual assignment and queue events from an authoritative scheduler
  source or explicitly mark that proof unavailable.
- Re-fetch authoritative state before signing, enforce identity consistency
  across requests, reject raced or superseded attempts, and hash downloaded
  artifact bytes before declaring artifact digests.
- Add integration tests against an authenticated disposable provider with
  an intentionally queued/stalled job, a test failure, an agent loss, and a
  successful terminal execution on the exact requested head.
- Store receipts atomically with existing gate evidence. The current
  `PutEvidenceBundle` upsert replaces the bundle for a work item/head;
  do not write one remote gate in isolation and overwrite other gate results.
- Only then expose pinned-key validation in the protected merge/release path.
  Unknown, missing, or unverifiable evidence remains blocking.

The v1 interface enables safe adapters without granting premature trust to
GitHub checks, webhooks, arbitrary CI log text, or user-supplied signatures.
