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
