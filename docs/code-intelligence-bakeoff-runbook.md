# Code-intelligence bake-off runbook

This runbook is the operational path for deciding whether `codebase-memory-mcp` can replace GitNexus in Dev Plane and, later, Adacavo.

## Decision rule

Do not remove GitNexus because a replacement is faster, cheaper, or easier to operate. The checked-in corpus is a curated set of **required core production paths**, not an exhaustive list of every file that may be relevant to a change. The safety question is therefore whether a backend finds every required path, not whether it returns the narrowest answer.

The gate is implemented by `packages/repo-intel/cmd/codeintel-promote`:

- every candidate run must complete successfully;
- candidate recall must be `1.00` on every scenario — every curated core path must be found;
- candidate recall must not regress against a successful GitNexus result on any scenario;
- precision and F1 are diagnostic/noise signals only because additional genuinely useful files are not necessarily present in the non-exhaustive gold set;
- latency and output-token cost are considered only after required-core recall is complete.

`promote: false` is a failed migration gate.

## Pinned benchmark toolchain

The reproducible worker pins:

- Node 24 (`node:24-bookworm` base image);
- Go 1.26.8;
- Python 3 from Debian Bookworm;
- `codebase-memory-mcp@0.11.0`;
- `gitnexus@1.6.12`.

Broad codebase-memory discovery scenarios use deterministic BM25 `search_graph(query=...)`; symbol/caller scenarios use graph traversal such as `trace_path`. Every codebase-memory query is explicitly scoped to the indexed project so one repository cannot leak results into another repository's score.

Benchmark output is enriched with:

- the actual backend/runtime versions;
- the exact Git SHA for every repository used in the run;
- the benchmark corpus version and SHA-256 of its exact bytes;
- the exact prompt, intent, expected core paths, and backend MCP calls for each scenario.

This means a stored `suite.json` is sufficient to identify what source and what benchmark definition produced the verdict.

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

The source repositories are mounted read-only. The benchmark image invokes the globally installed pinned binaries directly rather than resolving them again through `npx`. Mutable backend state is isolated under:

```text
/var/lib/codeintel/cbm
/var/lib/codeintel/gitnexus
```

and persisted in the `codeintel-benchmark-cache` volume. Results are written to:

```text
data/codeintel-bakeoff/
```

Important outputs:

- `suite.json` — run metadata, exact corpus provenance, repository revisions, backend observations, and tool versions;
- `<scenario>.json` — scorer input for an individual scenario;
- `raw/<scenario>/<backend>.txt` — raw backend output used to extract repository-tracked file matches.

To discard persistent backend indexes and force a completely cold run:

```bash
docker compose -f docker-compose.code-intelligence-benchmark.yml down -v
```

## Benchmark corpus

`benchmarks/code-intelligence/scenarios.json` currently covers four production-oriented paths:

- Adacavo proposal lifecycle and durable proposal-booking flow, including the legacy service implementation and transactional booking route;
- Nulang MIR-to-bytecode code generation and its core production callers in CLI, REPL, DAP, AOT, and the C embedding API;
- Nulang Cloud's Dev Plane runtime-provider boundary, local Wasmtime runtime, and Firecracker host-agent/VM factory;
- Dev Plane run admission through scheduler admission, task-readiness policy, agent-executor/runner budget delegation, budget engine, and worker wiring.

`python3 scripts/test_codeintel_corpus.py` protects the corpus contract: unique scenario IDs and core paths, exactly the two benchmark backends, explicit codebase-memory project scoping, and the pinned MCP parameter shapes.

Do not change an expected path merely because a backend fails to retrieve it. First verify the current production architecture and change the corpus only when source evidence shows that the ground truth changed.

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

It is pinned to the existing `linux/amd64` Woodpecker agent pool. The workflow installs the pinned toolchain, shallow-clones the four canonical `main` branches, removes the temporary Git credential and unsets the token before indexing/querying, uses isolated codebase-memory/GitNexus cache roots, runs the live bake-off, captures source/tool/corpus provenance, applies the promotion gate, and prints `suite.json` to the pipeline log.

The normal PR verification workflow is separate:

```text
.woodpecker/code-intelligence.yaml
```

That workflow never needs the private repository token and runs:

- the Python harness tests;
- the corpus-contract tests;
- the provenance tests;
- the `packages/repo-intel` Go tests under Go 1.26.8.

## Running the manual Woodpecker bake-off

After repository activation and secret creation:

1. Trigger the `code-intelligence-bakeoff` workflow manually in Woodpecker.
2. Confirm all four repositories cloned successfully.
3. Confirm the log reports the pinned versions before benchmarking.
4. Inspect the per-scenario backend lines for indexing/query errors.
5. Read the final promotion verdict.
6. Preserve the final `suite.json` from the log or configured log storage with the Woodpecker run identifier.

A failed backend, missing repository, broken/stale index, required-core-path miss, provenance failure, or `promote: false` means **do not remove GitNexus**.

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

### Candidate misses a required core file

Do not lower the benchmark expectation merely to make the candidate pass. First determine whether the expected file is genuinely part of the current production impact surface. If yes, treat the miss as a backend/query-quality issue. Improve the backend-specific query only if the new query describes the intent rather than directly enumerating the answer.

### Candidate returns many additional files

Do not treat additional files as an automatic failure. The corpus is intentionally non-exhaustive. Review precision/F1 and the raw output as noise indicators, but the migration safety gate is required-core recall.

### Both backends miss a required core file

Validate the corpus against source/semantic truth. If the expectation is correct, both backends are incomplete for that scenario. If the expectation is obsolete, update the corpus in a reviewed change with source evidence.

### Indexing succeeds but queries return another project

Confirm the project selector used by codebase-memory and inspect the raw response. Every codebase-memory scenario is explicitly project-scoped; cross-project output is a backend/query failure, not a passing result.

### Woodpecker cannot clone private repositories

Verify that `codeintel_github_token` exists as a Dev Plane repository secret, is permitted for manual events, and has read access to both private repositories. Do not broaden it to write/admin permissions.

### GitHub Actions shows zero-step failures

Do not use that status as source-validation evidence. The code-intelligence checks are deliberately portable to the local/container and Woodpecker paths above.
