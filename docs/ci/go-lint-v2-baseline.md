# Go lint v2 migration and legacy findings

As of October 14, 2026, the first strict golangci-lint v2.13.2 inventory
([CI run #38572024909](https://github.com/dporkka/dev-plane/actions/runs/38572024909))
found **272 findings in 15 modules**. They are not all within the scope of the
CI bootstrap work. The largest group was apps/api (142), followed by
packages/runtimes (20), packages/prfactory (19), and apps/worker (19).

## Enforcement contract

- `make lint-go` with no overrides remains the **strict full-repository**
  analyzer and Go vet gate. It is expected to fail until historical findings are resolved.
- For pull requests, CI supplies the exact base commit as `LINT_BASE_REV`;
  for pushes to main, it supplies the previous commit. GitHub checkout includes
  full history so the base is locally verifiable.
- `make lint-go LINT_BASE_REV=<verified-commit>` runs the **same analyzers**
  with `--new-from-rev` and `--whole-files`, failing for findings in
  changed Go files. This is a baseline ratchet, not a claim of strict lint
  compliance. Unchanged legacy modules do not block unrelated PRs.
- `scripts/tests/verify-lint-ratchet.sh` injects a temporary errcheck violation
  into a tracked Go file and requires the incremental linter to detect it.
  It restores the file after the check, even when the check fails.

## Backlog priorities

Prioritize actual correctness, security and resource lifecycle findings
(including unchecked close/rollback errors and OS process termination behavior)
before formatting or stylistic suggestions. Do not apply QF1008 mechanically to
`db.DB.Close()`: the explicit embedded selector avoids calling the wrapper's
own `Close()` recursively. Preserve serialized wire spellings, including
`"cancelled"`, unless a versioned migration is designed and tested.

Full cleanup and a switch back to a strict always-green CI lint gate require
separate, tested changes. Do not mark the full historical inventory resolved
because a new-issues ratchet passes.
