# Local Herdr agent inventory

Dev Plane can read the current local Herdr agent fleet without running an
additional scheduler or exposing Herdr's full terminal snapshots.

From the Dev Plane CLI:

```sh
dev-plane agents local
dev-plane agents local --json
```

The JSON command emits a stable, sorted list of agent kind, status, opaque pane
and workspace IDs, and focus state, plus the source protocol/version and total.
**It never exports** terminal titles, command output, scrollback, filesystem
paths, private session transcripts, environment variables, or credentials.

## Operating constraints

- The command is read-only and invokes only `herdr api snapshot` with fixed
  arguments, via a 5-second deadline. It cannot start, stop, rename, or prompt
  agents, and has no remote mode.
- Herdr must be installed and the local server must be running. Unsupported
  payloads or missing agents metadata fail with a non-zero exit status rather
  than reporting false zero agents. A genuine empty `agents: []` is supported.
- Use the same user account as the Herdr server so socket permissions remain
  enforced. Do **not** make its socket publicly accessible.
- This adapter is a **local telemetry building block**, not a Dev Plane web
  endpoint or a distributed telemetry service. A future dashboard integration
  should authenticate callers, preserve least privilege, and receive only the
  sanitized projection through a trusted local exporter.
- Neither task admission nor PR merge authorization depends on this telemetry.

## Pilot validation

```sh
cd apps/cli
go test ./cmd/dev-plane -run 'TestParseHerdr|TestAgentsLocalCLI' -count=1
go run ./cmd/dev-plane agents local --json
```

Validate that the count agrees with `herdr agent list`, check that unexpected
agent status values are surfaced safely or rejected, and ensure no session
transcripts appear in the output. Avoid logging the original raw snapshot.

## Dev Plane rollout

Keep the adapter local while testing. If fleet visibility improves incident
response, a later PR can add authenticated ingestion with an explicit time-to-live
for stale reports and attribution to the source host. Never treat stale session
telemetry as evidence of a successful test or deployment.
