# Code-intelligence bake-off runbook

This runbook is the operational path for deciding whether `codebase-memory-mcp` can replace GitNexus in Dev Plane and, later, Adacavo.

## Decision rule

Do not remove GitNexus because a replacement is faster, cheaper, or easier to operate. The replacement must first be at least as complete on the checked-in repository scenarios.

The gate is implemented by `packages/repo-intel/cmd/codeintel-promote`:

- every candidate run must complete successfully;
- candidate recall must be at least `0.80` on every scenario;
- candidate F1 must be at least `0.75` on every scenario;
- candidate recall must not regress against a successful GitNexus result on any scenario;
- candidate F1 must not regress against a successful GitNexus result on any scenario;
- latency and output-token cost are considered only after retrieval quality is safe.

`promote: false` is a failed migration gate.

## Pinned benchmark toolchain

The reproducible worker pins:

- Node 24 (`node:24-bookworm` base image);
- Go 1.26.8;
- Python 3 from Debian Bookworm;
- `codebase-memory-mcp@0.11.0`;
- `gitnexus@1.6.12`.

Benchmark output is enriched with the actual tool versions and exact Git SHA for every repository used in the run.

## Local/container run

Keep canonical clones in one directory using these checkout names:

```text
<root>/adacavo
<root>/nulang
<root>/nulang-cloud
<root>/dev-plane
```

Then run:

```bash
export CODEINTEL_REPOS_ROOT="$HOME/src"
docker compose -f docker-compose.code-intelligence-benchmark.yml build benchmark
docker compose -f docker-compose.code-intelligence-benchmark.yml run --rm benchmark
```

The source repositories are mounted read-only. Mutable graph/index state is stored in the `codeintel-benchmark-cache` volume. Results are written to:

```text
data/codeintel-bakeoff/
```

Important outputs:

- `suite.json` — complete run metadata, repository revisions, backend observations, and tool versions;
- `<scenario>.json` — scorer input for an individual scenario;
- `raw/<scenario>/<backend>.txt` — raw backend output used to extract file matches.

To discard persistent benchmark indexes and force a completely cold run:

```bash
docker compose -f docker-compose.code-intelligence-benchmark.yml down -v
```

## Woodpecker setup

Dev Plane must first be activated in Woodpecker so the forge webhook exists.

Create a repository secret named:

```text
codeintel_github_token
```

Use a fine-grained GitHub token with the minimum permissions required to clone the private benchmark repositories. It needs read access to repository contents for:

- `dporkka/adacavo`;
- `dporkka/nulang-cloud`.

`nulang-org/nulang` and `dporkka/dev-plane` are public and do not require private-repository access.

The live workflow is manual-only:

```text
.woodpecker/code-intelligence-bakeoff.yaml
```

It is pinned to the existing `linux/amd64` Woodpecker agent pool. The workflow installs the pinned toolchain, shallow-clones the four canonical `main` branches, removes the temporary Git credential before indexing/querying, runs the live bake-off, captures provenance, applies the promotion gate, and prints `suite.json` to the pipeline log.

The normal PR verification workflow is separate:

```text
.woodpecker/code-intelligence.yaml
```

That workflow never needs the private repository token and only runs the local harness/provenance tests plus the Go repo-intel tests.

## Running the manual Woodpecker bake-off

After repository activation and secret creation:

1. Trigger the `code-intelligence-bakeoff` workflow manually in Woodpecker.
2. Confirm all four repositories cloned successfully.
3. Confirm the log reports the pinned versions before benchmarking.
4. Inspect the per-scenario backend lines for indexing/query errors.
5. Read the final promotion verdict.
6. Preserve the final `suite.json` from the log or configured log storage with the run identifier.

A failed backend, missing repository, stale/broken index, or `promote: false` means **do not remove GitNexus**.

## Migration sequence after a passing result

A single passing run is necessary but not sufficient for Adacavo because GitNexus currently participates in safety/freshness workflows there.

Recommended sequence:

1. Obtain a clean `promote: true` run on the pinned worker.
2. Repeat the run once from a cold benchmark cache to rule out accidental index-state dependence.
3. Remove the disabled GitNexus fallback from Dev Plane only.
4. Run Dev Plane repo-intel tests and its normal verification profile.
5. Use codebase-memory as the only graph backend in Dev Plane for normal agent work and observe freshness/reliability.
6. Run the benchmark again after that bake-in period.
7. Only then migrate Adacavo's GitNexus-specific safety/freshness scripts and instructions.
8. Keep the GitNexus removal in Adacavo as a separate PR so rollback is trivial.

Nulang and Nulang Cloud do not need a GitNexus-removal step from this migration branch; their new project configuration already points at codebase-memory.

## Failure triage

### Candidate misses expected files

Do not lower the benchmark expectation merely to make the candidate pass. First determine whether the expected file is genuinely part of the production impact surface. If yes, treat the miss as a backend quality issue or improve the backend-specific query without making the query encode the answer.

### Both backends miss an expected file

Validate the corpus against source/semantic truth. If the expectation is correct, both backends fail that scenario's absolute quality floor. If the expectation is obsolete, update the corpus in a reviewed change with source evidence.

### Indexing succeeds but queries return another project

Confirm the repository path/project selector used by the backend and inspect the raw response. The benchmark's source SHA still identifies the intended checkout; a cross-project answer is a backend/query failure, not a passing result.

### Woodpecker cannot clone private repositories

Verify that `codeintel_github_token` exists as a Dev Plane repository secret, is permitted for manual events, and has read access to both private repositories. Do not broaden it to write/admin permissions.

### GitHub Actions shows zero-step failures

Do not use that status as source-validation evidence. The code-intelligence checks are deliberately portable to the local/container and Woodpecker paths above.
