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

A token must have **read-only** Pull requests/Contents/Checks access to the selected repositories. Public repositories may work without a token but unauthenticated GitHub rate limits are low. Never store the token in source, shell history, issues, CI logs, or report artifacts. The script never calls mutation endpoints and writes no repository state. The report includes publicly visible PR links and short commit SHAs, but review confidentiality before sharing reports that include private repositories.

**Terminology:** cohort totals count PRs *created in the lookback*, classified by their current state; they are not a throughput metric for PRs merged during the lookback. `All open` includes older PRs from all creation dates. `Stale` means no `updated_at` activity in the selected interval; it does not imply safe closure. Checks are inspected at the recorded PR head SHA of the top `--inspect` items; because the report does not re-fetch the PR after inspection, a moving PR head can make a snapshot stale. `passing-observed-only` does not establish branch protection, mandatory contexts, Woodpecker, CodeQL, test correctness, human approval, staging smoke tests, or production deployment. `no-evidence`, `pending`, `failed`, and `incomplete` are never passing states. `not-inspected` means no CI calls were made for that PR. Results can change immediately after snapshot collection.

**Read-only CI drilldowns (merged PR #209):** Inspected items include up to five non-success GitHub check runs and commit statuses, sorted by observed outcome priority, with provider detail links when they are well-formed HTTPS URLs. The JSON includes `attentionChecks`, `attentionTotal`, and `evidenceComplete`. These are status observations, not a verified first failed executable step or a root-cause determination. Incomplete/missing pagination counts, including truncated combined commit statuses, fail closed to `incomplete-evidence`; the drilldown is then withheld. The combined-status request explicitly fetches up to 100 statuses. No action is taken on a provider or repository.

## Advisory cross-repository check co-occurrences

The portfolio report includes a sharedFailureSignals object (signals and total)
in JSON and a human-readable cross-repository section in Markdown. A signal
requires the same **provider status source, exact check name and failed
conclusion** in at least two distinct repositories among the PRs inspected by
this bounded report. Multiple PRs in one repository do not manufacture an
additional affected repository. Only observed failure/error/timeout/startup/
action-required outcomes participate. Canceled, pending, missing, uninspected,
and incomplete evidence do not yield positive correlation signals.

The displayed groups are limited to ten, sorted by affected repository count.
Each PR contributes only its existing five highest-priority attention checks;
this is **sampled coverage**, not exhaustive failure discovery. Counts include
only observed PRs within that bounded subset. Even a shared startup_failure
check name does not prove a common runner, cause, infrastructure incident, or
dependency. Review actual provider execution logs and runner assignments
before acting. Re-fetch candidate PR heads if a release decision is involved.
This report remains read-only, does not create incidents, and grants no merge
or deployment authority.

## Advisory CI diagnosis in portfolio reports

The read-only report now includes `ciDiagnosis` beside the existing `ci` value for each open PR. The JSON shape contains `status`, `category`, `confidence`, `summary`, and `nextAction`, and Markdown displays the diagnostic category and suggested next inspection. Uninspected PRs are explicitly `not-inspected`; check-run pagination beyond the inspected first page is `incomplete-evidence`, never green.

Categories identify **observed states**, not proven causes: `provider-startup` reflects a reported `startup_failure`, `canceled` reflects cancellation, `execution-failure` requires reading the first failing step to distinguish application from infrastructure, and `pending` does not establish a scheduler or agent fault. `observed-success` is still only an advisory snapshot and cannot certify required workflows, executing steps, reviews, branch protection, or deployment. No diagnosis authorizes retry, cancellation, branch updates, merge, or deployment.

This slice deliberately reads only the GitHub Checks and Status APIs. Woodpecker queue/task evidence, worker assignment, authentic attempted steps, and signed remote CI receipts must be integrated through the separately reviewed Dev Plane verifier/adapter stack; never infer those facts from the GitHub conclusion alone.

## Manual stacked-PR CI qualification (opt-in, advisory)

After `.github/workflows/stack-qualification.yml` has been reviewed and merged to `main`, an operator may deliberately run the **manual-only** workflow against one open, same-repository stacked PR:

```bash
gh workflow run stack-qualification.yml --ref main -f pr_number=202
```

The dispatcher runs from trusted `main`, reads the PR's actual head/base commit SHAs from the GitHub API, rejects forks/closed PRs/mismatched identities, and checks out the immutable head SHA rather than a moving branch name. It fails if Git reports that the PR's base SHA is not an ancestor of that head. The selected PR's code is tested with the same repository `make` test, build, lint and typecheck commands, but no deployment credentials or write permissions are provided to the job. A separate final job checks out the trusted resolver at the dispatch SHA and **reconfirms** the current PR head/base via the read-only GitHub API. If either changed during testing, the overall workflow fails rather than reporting stale success. This is a point-in-time check, not a permanent guarantee after the run. There is **no** `pull_request` trigger and no automatic queue fanout.

**Protected verification-plan preflight:** Before exporting any candidate SHA to the test runner, trusted `main` code paginates the GitHub PR changed-file API (up to its documented 3,000-file bound), fails on incomplete inventories, rejects modifications/renames involving Makefiles, workflow/actions definitions, repository scripts, runtime/test manifests and other verification control files, and re-reads the PR head/base to reject changes during the preflight. PRs modifying those files need separate, independently reviewed CI qualification; the manual advisory run deliberately offers no override. This conservative policy also excludes legitimate tooling/dependency changes. It does **not** prove the remaining candidate code cannot affect tests or provide trusted third-party execution attestation.

**Do not treat this workflow as a canonical PR status or release approval.** A manual workflow event is associated with the dispatch ref (`main`) in GitHub; the checkout and step summary, not a green PR status badge, document which head was actually tested. Real PR review, mandatory contexts, independent authorization, exact-head executable evidence, staged merge-tree testing and any production qualification remain separate. Do not change PR bases, fabricate status checks, or merge solely to obtain a green manual run. If the head or base moves, require fresh qualification.

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
