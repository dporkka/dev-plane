# Production Readiness Checklist

This checklist tracks the minimum gates required before AI Dev Control Plane should run untrusted repositories or production-impacting workflows.

## Completed In Current Code

- Agent-run tool calls are capability-gated before workspace execution.
- The composed API server injects an audit-backed capability kernel into dangerous workspace operations; human workspace actions and agent tool checks persist `capability_check` audit rows with actor and organization context.
- Unknown capability operations default to approval required.
- Arbitrary shell execution and git pushes require approval by default.
- Test execution has a distinct allow-listed capability operation.
- Approval-required agent tool calls create approval records and pause the run instead of continuing.
- HTTP workspace writes, patches, and shell commands are capability-gated before side effects.
- HTTP workspace shell command timeouts kill the process group rather than waiting on orphaned child processes.
- Docker runtime provider implements create, exec, file read/write, patch, snapshot, restore, status, log streaming, and cleanup with no runtime network, named workspace volumes, read-only rootfs, dropped capabilities, no-new-privileges, and CPU/memory/PID limits.
- Worker `tasks.approved` handling provisions configured runtime sessions and persists runtime provider/session metadata on workspace rows.
- HTTP workspace read, write, patch, exec, directory-list, and dev-service start/stop endpoints route Docker-backed workspace rows through `runtimes.Provider` instead of requiring local worktree paths.
- HTTP workspace dev-service start/stop is implemented for local trusted worktrees and Docker-backed runtime sessions; start and stop are capability-gated. Local service PID/log/command metadata is stored under the API-owned workspace runtime base directory, while runtime-session service metadata remains inside the sandbox under `.dev-plane/services/<service_id>`.
- Agent-run tools dispatch through `runtimes.Provider` for Docker-backed workspace rows and keep local filesystem tools for trusted local workspaces.
- `packages/runtimes` includes a live Docker integration suite gated by `RUN_DOCKER_INTEGRATION=1` for host-path isolation, no-network execution, cleanup, and resource-limit checks.
- Secret references and versioned secret values are stored in dedicated tables; values are encrypted with AES-256-GCM using `SECRET_ENCRYPTION_KEYS`, never returned by metadata endpoints, rotated by creating a new active version, and audited on write/read/rotate.
- `packages/db` applies the full Goose migration chain against SQLite in tests, including secret storage, agent mailbox tables, and persisted review reports.
- Agent runs use a model-driven structured action loop instead of a hard-coded tool sequence. Model turns produce one JSON action (`tool_call`, `final_response`, `handoff`, or `request_approval`), and tool calls still flow through step persistence, capability checks, approval pause behavior, budget checks, runtime-provider dispatch, and event streaming.
- Model-driven `request_approval` actions now persist approval records, publish `approval.requested`, and pause the run with a durable reason instead of only changing run status.
- Runner-level tests cover model-requested pause/approval creation, denied tool operations that fail the run without creating an approval, and resumed runs loading prior step history while continuing step numbering.
- Agent handoffs are persisted to the `agent_messages` mailbox table, loaded into later model prompts for the addressed role or broadcast recipients, consumed exactly once by the worker, and used to queue follow-on role runs with trace metadata.
- Agent runs publish canonical `agents.run.started`, `agents.run.completed`, and `agents.run.failed` lifecycle events in addition to per-run `runs.{id}.*` events, so worker subscriptions can observe runner completion.
- Worker `runs.triggered` handling dispatches queued initial and follow-on runs to the shared API agent executor, which wraps the existing model-driven runner with policy, budget, audit, event, workspace-tool, and runtime-provider wiring.
- HTTP spec generation now persists a durable `task_specs` row synchronously, uses UUID spec IDs compatible with the database schema, transitions the task to `spec_review`, and publishes `tasks.updated` with generated spec metadata.
- HTTP spec approval publishes `tasks.approved`; worker `tasks.approved` handling provisions the workspace, creates the queued implementer run, publishes `runs.triggered`, and republishes an existing queued run on retry instead of duplicating runs.
- Worker `agents.run.completed` handling no longer synthesizes review completion. If no mailbox handoff is waiting, it runs the reviewer service, persists a `review_reports` row, and publishes `review.completed` from the generated report.
- Reviewer security checks now run the registered Gitleaks, Trivy, and Semgrep adapters for local worktrees, convert scanner findings into review findings, make high/critical security findings non-approvable, and surface scanner unavailability as an explicit medium-risk security finding.
- Worker `approval.approved` handling resumes paused agent runs for `risky_action` and `capability:*` approvals by requeueing the run, updating the task to running, and publishing `runs.triggered`; it still reserves PR creation for explicit `pr_create` approvals.
- HTTP approval responses publish `approval.approved` or `approval.rejected` events after updating the approval row, and rejected responses mark the task failed immediately. Worker approval handling only creates PRs for explicit `pr_create` approvals, with tests covering approved, rejected, non-PR, non-reviewable, and PR creation error paths.
- PR factory no longer creates fake local PR records when GitHub is unavailable. It requires a configured GitHub gateway/token, pushes the workspace branch with non-interactive Git, calls the GitHub PR API, and only persists a PR record after GitHub returns a PR.
- Direct model providers are HTTP-backed for OpenAI, Anthropic, Gemini, Groq, and Fireworks. The router passes the selected model into each provider call, records usage/cost estimates from provider responses, supports structured JSON response mode where the provider API supports it, and keeps provider calls locally testable with injectable HTTP clients.
- Event stream definitions are centralized and locally tested so every declared task, agent, run, review, approval, PR, webhook, and audit subject is covered by a configured JetStream stream.
- GitHub webhook handlers require `GITHUB_APP_WEBHOOK_SECRET`, reject missing or invalid `X-Hub-Signature-256` headers, and publish accepted non-ping deliveries to `webhooks.received`; publish failures return a retryable HTTP error instead of silently acknowledging dropped work.
- Repository connection now validates GitHub owner/name components before constructing clone URLs, and the web repository screen sends the API's owner/name contract with mutation error handling and list refresh for connect, sync, and disconnect actions.
- Repository intelligence no longer returns `ErrNotImplemented` for symbol search or no-dependency repositories; the lightweight indexer now extracts best-effort lexical symbols, indexes source content, and parses Go/npm dependencies with tests.
- Security scanner adapters now parse Gitleaks, Trivy, and Semgrep JSON output into structured findings with normalized severity, confidence, file, line, remediation, and summary counts instead of returning only opaque raw output.
- All Go modules in `go.work` pass `go test -buildvcs=false ./...` from their module directories.
- `apps/web` passes `npm audit`, `npm run typecheck`, `npm run lint` (warnings only), and `npm run build`.

## Hard Blockers

- [x] Run the live Docker runtime integration suite in a Docker-enabled environment and capture passing evidence for the target host configuration.
- [ ] Run a live end-to-end agent execution against a configured model provider and runtime, from task approval through follow-on handoff execution, and capture passing evidence.
- [ ] Run a live PR creation check with `GITHUB_TOKEN` against a disposable repository and capture evidence that branch push, GitHub PR creation, and local PR record persistence all succeed.
- [x] Run the gated Postgres migration verification with `POSTGRES_TEST_DATABASE_URL` and capture passing evidence for the production database engine.
- [ ] Add remaining live end-to-end coverage for task approval through real worker execution after approval, including a paused run resumed by an approval response. Local handler/runner tests now cover task approval event emission, workspace/run creation, run trigger publication, approval response events, paused-run requeue, PR creation dispatch, model-requested pause, resumed history, and denied operations.

## Completed Fixes

- Fixed Docker workspace copy path: `docker cp` now copies the *contents* of the staged repo into `/workspace` rather than copying the `repo` directory itself. The source path uses `stagingRepo + "/."` because `filepath.Join(stagingRepo, ".")` collapses to `stagingRepo`.
- Fixed GitHub gateway unit tests to use a configurable `apiBaseURL` pointing at `httptest` servers instead of hitting the live GitHub API.

## Verification Evidence

Captured on this checkout (Ubuntu Linux, Docker 29.6.0, Go 1.25, Node 20, NATS running via `docker-compose`):

- `make test` — all Go modules and `apps/web` pass.
- `make test-race` — no race conditions detected.
- `make lint` — Go vet and web ESLint pass.
- `make build` — API, worker, runner binaries and Next.js production build succeed.
- `cd apps/web && npm audit` — 0 vulnerabilities.
- Docker runtime integration (host-path isolation, no-network, cleanup, resource limits):

  ```bash
  cd packages/runtimes
  RUN_DOCKER_INTEGRATION=1 go test ./... -v
  ```

  Result: `TestDockerProviderIntegrationIsolationAndCleanup` and all other runtime tests pass.

- Postgres migration verification (all 20 migrations applied successfully):

  ```bash
  # Start a Postgres 16 container
  docker run -d --name aicp-postgres-test -e POSTGRES_USER=aicp \
    -e POSTGRES_PASSWORD=aicp-dev-password -e POSTGRES_DB=aicp \
    -p 5432:5432 postgres:16-alpine

  cd packages/db
  POSTGRES_TEST_DATABASE_URL=postgres://aicp:aicp-dev-password@localhost:5432/aicp?sslmode=disable \
    go test ./... -run TestRunMigrationsPostgres -v
  ```

  Result: migrations `001` through `020` applied cleanly.

## Remaining Live Gates

These gates require external credentials and cannot be completed in a credentials-free environment:

1. **Live model-provider end-to-end run**
   - Requires a valid `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, or other supported provider key in `.env`.
   - Steps: create a task, approve the generated spec, observe the worker provision a Docker workspace, run the implementer, and confirm follow-on handoffs execute.
   - Suggested smoke test repository: a disposable public repo with a trivial `README.md` change.

2. **Live GitHub PR creation**
   - Requires `GITHUB_TOKEN` with `repo` push + PR scope in `.env`.
   - Requires a disposable repository where the bot can push a branch and open a PR.
   - Steps: run a task through approval, approve the `pr_create` approval, and verify the branch is pushed, the PR exists on GitHub, and a `pull_requests` row is persisted.

3. **Live worker resume after approval**
   - Requires the full stack running (`make dev` or equivalent) plus a model provider.
   - Steps: trigger an implementer run that requires a risky-action approval, respond `approved`, and confirm the worker requeues the run and continues step numbering.

## Verification Gates

- `go test ./...` across every Go module in `go.work`; use `-buildvcs=false` for `apps/api` until this checkout has valid Git metadata.
- Web dependency audit, typecheck, lint, and production build for `apps/web`.
- Runtime integration tests proving Docker sessions cannot access host paths or network unless granted: `RUN_DOCKER_INTEGRATION=1 go test ./...` in `packages/runtimes`.
- Postgres migration verification: `POSTGRES_TEST_DATABASE_URL=... go test ./...` in `packages/db`.
- Policy tests proving write, patch, shell, commit, push, PR, merge, deploy, network, and secret operations cannot bypass approval/deny rules.
- Event tests proving `tasks.*`, `agents.>`, `runs.*`, `review.*`, `approval.*`, `pr.*`, `webhooks.*`, and `audit.>` subjects publish to configured streams.
