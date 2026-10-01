# Dev Plane Engineering Contract

This file is the repository-wide contract for human and AI contributors. Keep implementation-specific playbooks in skills or docs; keep durable engineering rules here.

## Mission and authority boundaries

Dev Plane is the orchestration authority for task/run lifecycle, isolated workspaces, agent assignment, approvals, evidence, verification state, review policy, landing, and release gating.

The repository remains authoritative for how its code is built and verified. Do not move repository-specific build semantics into the control plane when they can remain behind `devplane.yaml`, Make, Cargo, npm, Dagger, or another repo-native command.

Interactive clients such as ACP adapters or `devx` are clients of Dev Plane. They may own protocol negotiation, auth, session UX, streaming, permission prompts, attach/resume, and cancel. They must not become a second orchestration authority.

## Non-negotiable invariants

1. **Exact-revision evidence.** Verification evidence is valid only for the exact candidate HEAD it was produced against. A new commit invalidates prior revision-bound evidence. Preserve `EvidenceBundle.ValidateHead` semantics.
2. **Repository-native verification.** Agent claims are never proof. Required verification is the command declared by this repository and/or CI.
3. **Fail closed on permissions.** Unsupported, ambiguous, or denied permission escalation must not silently become approval.
4. **Isolated work.** Parallel implementation work uses isolated worktrees/containers. Do not let multiple agents mutate one working tree.
5. **Smallest safe change.** Prefer minimal behavior-preserving changes over opportunistic refactors.
6. **Regression proof.** For behavior changes and bug fixes, add a failing regression test first when practical, then implement the smallest fix and rerun the relevant suite.
7. **No weakened gates.** Never make CI green by deleting assertions, skipping tests, broadening mocks, downgrading required fixtures, or marking required checks optional.
8. **No fake integration evidence.** If a task requires PostgreSQL, NATS, a browser, a real provider, or another concrete fixture, a mock/substitute does not satisfy that requirement.
9. **Risk determines review.** Security, auth, policy, tenancy, migrations, landing, deployment, and control-plane invariants receive stricter verification and review than docs or isolated presentation changes.
10. **Git is code truth; Dev Plane is execution truth.** Git records what changed. Dev Plane records what task/run verified which revision. Do not conflate the two.

## Standard workflow

For non-trivial changes:

1. Read the issue/task and identify explicit acceptance criteria.
2. Inspect `devplane.yaml` and the relevant code/tests.
3. For unfamiliar code, use GitNexus to identify execution flows and blast radius before editing symbols.
4. Create or use an isolated worktree/workspace.
5. Reproduce the failure or encode the desired behavior in a test before the implementation when the change affects behavior.
6. Implement the smallest safe change.
7. Run the narrowest relevant verification first, then escalate through the verification ladder required by the changed surface.
8. Run GitNexus change detection before handoff/commit when GitNexus is available.
9. Bind reported evidence to the exact HEAD that was verified.
10. Use independent review for medium/high-risk logic and all surfaces required by `devplane.yaml`.
11. Treat repository CI as the final automated merge authority.

## Canonical verification ladder

Use `bash scripts/verify.sh <profile>` locally and in automation. Profiles are cumulative only where explicitly documented by the script.

- `static` — Go lint/vet, web lint + typecheck, SDK typecheck.
- `test` — Go/SDK tests plus web tests.
- `build` — production builds.
- `standard` — static + test + build. This is the normal pre-PR contract.
- `race` — standard + Go race tests.
- `integration` — race + credential/fixture-backed integration tests.
- `live` — integration + live end-to-end gates; requires the documented external credentials/services.

Do not substitute a lower profile for a higher profile required by `devplane.yaml`, the task acceptance criteria, or CI.

## Risk and review policy

Use the repository contract in `devplane.yaml` as the machine-readable source of truth. As a default mental model:

- Low risk: docs/comments/presentation-only changes — deterministic checks may be sufficient.
- Medium risk: ordinary business logic/refactors — independent review is preferred.
- High risk: auth, permissions, policies, repository protocol, evidence/verification, security scanning, migrations, CI/landing/release — require the repository's full gate plus independent review and any declared human approval.
- Critical/destructive operations: preserve explicit human approval and fail closed.

Do not infer that a change is low risk merely because the diff is small.

## GitNexus workflow

GitNexus is the code-intelligence layer, not the engineering constitution.

When GitNexus is available:

- Before modifying an existing function, class, or method, run upstream impact analysis for the symbol and note the blast radius.
- For unfamiliar behavior, use GitNexus execution-flow queries/context before grep-only exploration.
- Treat HIGH or CRITICAL impact as a signal to narrow scope and increase verification/review.
- Before handoff/commit, run change detection against the intended base (normally `main`) and confirm only expected symbols/flows changed.
- Use GitNexus rename/refactor operations rather than textual find-and-replace for symbol renames.

Detailed GitNexus procedures live under `.claude/skills/gitnexus/`.

If GitNexus is unavailable in the execution environment, do not fabricate its results. State the limitation, keep the change narrow, and rely on repository tests/CI and independent review before merge.

## Repository commands

Prefer checked-in commands over ad hoc equivalents.

Development and services:

```bash
make dev
make docker-up
make docker-down
make docker-logs
```

Verification:

```bash
bash scripts/verify.sh static
bash scripts/verify.sh test
bash scripts/verify.sh build
bash scripts/verify.sh standard
bash scripts/verify.sh race
bash scripts/verify.sh integration
bash scripts/verify.sh live
```

Useful narrower targets include:

```bash
make test-api
make test-cli
make test-worker
make test-packages
make lint-go
make lint-web
make build-api
make build-cli
make build-worker
make build-runner
make build-web
```

Database and code generation:

```bash
make migrate
make db-status
make gen-db
```

Destructive targets such as `make db-reset` and `make docker-down-volumes` require explicit awareness of their destructive effect.

## Definition of done

A change is not complete merely because code was edited or an agent says it is complete. Before handoff, provide evidence for all applicable items:

- Acceptance criteria are implemented.
- Regression/behavior tests cover the changed behavior when applicable.
- The required verification profile passes for the exact HEAD being handed off.
- Required real fixtures (for example PostgreSQL or browser/system tests) were actually exercised.
- GitNexus change detection is clean/expected when available.
- High-risk paths received the independent review / human approval required by `devplane.yaml`.
- No required gate was skipped, weakened, or replaced by a non-equivalent substitute.
- The working tree/workspace is clean enough for a deterministic handoff.

## Never do

- Do not claim tests passed unless they were run against the reported revision.
- Do not merge or promote based on evidence from an older HEAD.
- Do not silently reinterpret acceptance criteria to fit the implementation.
- Do not hide failures as "pre-existing" without evidence.
- Do not add another orchestration path when the requirement belongs in Dev Plane, the repository contract, or an existing adapter.
- Do not duplicate long-lived policy across `AGENTS.md`, `CLAUDE.md`, editor config, and shell prompts. Keep `AGENTS.md` canonical and make tool-specific files point here.
