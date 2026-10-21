# Portfolio integration loop (Adacavo, Nulang, Nulang Cloud, Dev Plane)

## Goal

Optimize **verified, deployed outcomes**, not generated branches, prompts, commit counts, or draft PRs. A PR opened by an agent is inventory, not delivery. Do not automatically merge, close, rebase, or deploy an item based on this report.

## Read-only report

Run from a trusted Dev Plane checkout using Node.js 22 or newer:

```bash
node --test scripts/pr-queue-report.test.mjs
GH_TOKEN="$(gh auth token)" node scripts/pr-queue-report.mjs > /tmp/pr-queue.md
# To consume the same snapshot in a dashboard:
GH_TOKEN="$(gh auth token)" node scripts/pr-queue-report.mjs --format json > /tmp/pr-queue.json
```

Pass `--repos owner/repo,owner/repo` to change the portfolio. `--days 7` sets the creation-cohort lookback, `--stale-days 3` marks unchanged open PRs, `--inspect 5` checks the first five priority PR heads per repository, `--show 10` limits displayed priority rows, and `--max-pages 20` limits GitHub API pagination. Increase `--max-pages` if the command reports a truncated queue. The report is printed only after *every* repository and CI query succeeds; an HTTP failure or incomplete inventory returns a nonzero exit status, not misleading partial totals.

## Exact-head CI evidence triage

For every inspected PR, the reporter fetches check runs and commit statuses at the
listed head SHA, then **re-fetches the live PR head**. If that head changes or
the PR is closed during collection, the entire snapshot fails without output.
If an individual check run advertises a different head SHA, the snapshot also
fails closed. This narrows, but cannot eliminate, races after the final check.

The JSON report includes `ciTriage` and up to ten `failedChecks` records
with source, check/context name, and GitHub conclusion/state. These are
**observations, not a first failing step or root-cause diagnosis**. Proposed
operator actions are:

| CI observation | Triage label | Required next action |
| --- | --- | --- |
| No observed contexts | `investigate-workflow-trigger-or-runner` | Confirm event trigger, job admission, runners and CI configuration |
| `startup_failure` | `investigate-runner-admission` | Diagnose checkout, scheduler/runner admission and setup before editing product code |
| `timed_out` | `investigate-timeout` | Inspect job timing and resource evidence; do not automatically relax timeouts |
| Other failing check | `inspect-failing-job-logs` | Obtain first confirmed failing step and reproduce before a repair |
| Pending | `wait-for-terminal-checks` | Wait for terminal evidence |
| Missing/unknown/truncated evidence | `inspect-missing-or-unrecognized-evidence` | Fetch the missing evidence, never assume green |
| Observed passing | `verify-required-gates` | Separately verify required contexts, reviews, security gates and deployment readiness |

The reporter does not download job logs, infer flaky tests, retry jobs, repair
source files, or certify promotion. Any future repair orchestrator must take
explicitly scoped actions only after obtaining a confirmed failing step and
revalidating the candidate head.

A token must have **read-only** Pull requests/Contents/Checks access to the selected repositories. Public repositories may work without a token but unauthenticated GitHub rate limits are low. Never store the token in source, shell history, issues, CI logs, or report artifacts. The script never calls mutation endpoints and writes no repository state. The report includes publicly visible PR links and short commit SHAs, but review confidentiality before sharing reports that include private repositories.

**Terminology:** cohort totals count PRs *created in the lookback*, classified by their current state; they are not a throughput metric for PRs merged during the lookback. `All open` includes older PRs from all creation dates. `Stale` means no `updated_at` activity in the selected interval; it does not imply safe closure. Checks are inspected only on the exact current PR head of the top `--inspect` items. `passing-observed-only` does not establish branch protection, mandatory contexts, Woodpecker, CodeQL, test correctness, human approval, staging smoke tests, or production deployment. `no-evidence`, `pending`, `failed`, and `incomplete` are never passing states. `not-inspected` means no CI calls were made for that PR. Results can change immediately after snapshot collection.

## Stacked verification branches

When a CI repair and a feature are verified on a disposable combined candidate, **do not merge the disposable verification PR**. First merge the separately reviewed repair through the normal gates, then put the feature's unchanged source blobs onto the newly accepted `main` commit, verify the source file hashes, and run checks against the feature PR's **new exact head**. A successful run on the disposable combined tree is useful compatibility evidence but is not a substitute for this final run. Close the verification-only PR after its evidence has been recorded and the real feature PR has been accepted; never let it become product history.

## Execution policy: finish before starting

For the next two weeks, start with **at most two implementation lanes and one dedicated integration/CI-repair lane across the active portfolio**. This is an operating hypothesis, not a new branch-protection rule. Avoid writing additional queue-automation products until the pilot improves completion speed and defect rates.

1. **Select:** pick one customer-critical outcome, define its end-to-end acceptance test and business value; favor Adacavo lead -> availability -> proposal -> contract -> invoice -> payment. Keep a separate narrowly scoped Nulang release/reliability lane only when capacity allows.
2. **Inventory:** run the report; identify duplicates, superseded experiments and draft stacks. Do not close PRs without owner review. Mark candidates for same-head validation separately from candidates blocked by missing CI or a baseline dependency.
3. **Repair the verifier first:** for a candidate with no jobs, repair runner admission/checkout/workflow setup before editing business logic. For a red test, capture the *first confirmed* failure from logs and reproduce with a focused test. Preserve source-vs-execution evidence distinction.
4. **Promote on evidence:** require current-head focused tests, repo-wide relevant checks, codeowner/security review when applicable, cross-branch integration checks for stacked changes, and production-like golden-path tests. Record exact SHA, commands, run IDs, and exclusions. Never infer a green gate from a preview-ready notification.
5. **Integrate one at a time:** human decides on merge, rollback and deployment. Re-run on a new head; do not use parent/different-branch evidence as proof for a candidate.
6. **Close the loop:** confirm smoke tests, production telemetry, defects, and actual customer impact. If the change delivered no measurable outcome, reassess priority.

## Pilot scoreboard

Review twice weekly against the same repo scope:

- **Created-cohort disposition:** merged/open/closed-unmerged percentages for PRs created seven days earlier (allow at least seven days of maturation before comparing cohorts).
- **Backlog:** all open PR count, draft count, stale count, and oldest updated dates.
- **Delivery:** time from first PR commit to green required gates, merge, and production smoke success. Gather from protected systems separately; the report does **not** claim to measure this.
- **Quality:** red/blocked/no-run checks, escaped defects, rollback frequency and recovery time.
- **Value:** completed Adacavo demos/transactions, Nulang release correctness and benchmark stability, Nulang Cloud billing/agent reliability, Dev Plane operator minutes saved.

If completion rate, CI reliability, and time-to-production do not improve after two weeks, revert the concurrency policy and measure what is actually blocking integration before adding orchestration complexity.

## Project-specific promotion focus

| Project | First promotion proof | Do not substitute |
| --- | --- | --- |
| Adacavo | Exact-head canonical CI plus Postgres tenant-isolation and browser lead-to-payment golden path | Draft PR, source-only Jest tests, preview readiness |
| Nulang | Formatting, Clippy, full applicable Rust tests plus compiled durability/recovery tests | Passing targeted unit tests alone |
| Nulang Cloud | Exact-head Rust workspace + billing/Stripe state-machine and isolation tests | A successful Vercel preview or developer-safety contract |
| Dev Plane | CI repair baseline, integrated candidate checks, deterministic agent sandbox/promotion tests | Tests on feature and repair branches considered independently |
