# Executable verification evidence

Dev Plane persists a structured execution attempt for each locally executed
`devplane.yaml` verification profile, in addition to the existing command
output and the exact candidate head. This is an incremental Buildkite-style
execution-evidence contract, **not a third-party CI attestation**.

## Record semantics

A gate's `execution` object includes:

- `session_id`: Dev Plane runtime workspace session; **not** independently
  verified worker identity.
- `attempt_id`: random identifier for this invocation, including retries.
- `outcome`: `completed` if the runtime returned a command result without
  a transport/runtime error; otherwise `runner_error`.
- `exit_code`: only available for completed command results. Nonzero values
  denote a test/build failure, not a scheduling failure.
- `started_at` and `finished_at`: timestamps from the Dev Plane verifier
  process, not remote worker attestations.
- `duration_ms`: runtime-reported command duration when positive, otherwise
  measured wall time between invocation and return.

The verifier checks Git HEAD and the clean working tree before and after each
command. For executable gates, `EvidenceBundle.ValidateHead` now requires a
completed attempt, a zero exit code, a session and attempt ID, and consistent
timings. Existing evidence with only `status=passed` is **not sufficient**
for this check. An old stored bundle must be reverified on its exact candidate
head rather than upgraded by adding synthetic metadata.

Failures remain persistable for investigation: a completed command with
nonzero exit code is `failed` + `completed`, while a runtime/dispatch
failure is `failed` + `runner_error`. Neither satisfies `ValidateHead`.
Independent-review and human-approval gates use separate authorization
mechanisms and do not pretend to be executable shell commands.

## Trust boundary and follow-up

These values are produced by the local runtime adapter. They do **not**
establish that a Woodpecker/Buildkite job was scheduled, ran on a particular
agent, or emitted a trusted remote terminal result. That integration still
requires immutable pipeline and runner IDs, queue/assignment/execution
transitions, an exact source SHA, provenance for artifact digests, and a
verified terminal callback.

Do not automatically treat an external green GitHub status as execution proof,
or allow these records to bypass human approvals, tenant isolation, migration
checks, or the independent remote CI gates.
