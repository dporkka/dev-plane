# Code Intelligence Stack

Dev Plane uses a layered code-intelligence model so agents use the cheapest precise signal first and reserve graph analysis for questions that need it.

## Ownership

Shared services:

- `codebase-memory-mcp`: persistent structural graph, architecture, call paths, impact analysis, cross-repository relationships, and conceptual fallback.
- Zoekt: fast lexical and regex search over the canonical local repository set.

Per-agent/worktree tools:

- Native language server (`rust-analyzer`, `gopls`, TypeScript language service, Pyright, etc.) for branch-local semantic truth.
- `ast-grep` for structural search and repeatable codemods.
- `rg`/`fd` for cheap local lookup.
- compiler, tests, linters, and CI for executable verification.

On-demand only:

- Joern for deep source-to-sink/data-flow/security analysis.
- SCIP should not be operated as a separate service until a measured cross-repository semantic-indexing gap justifies the extra index lifecycle.

## Vendor-neutral routing contract

`packages/repo-intel` owns the stable routing vocabulary. Product code and agents should request an intent instead of depending directly on a vendor's MCP tool names.

Supported intents are:

- `locate` — exact or conceptual discovery;
- `understand` — symbol or architecture understanding;
- `impact` — change blast radius;
- `refactor` — structural transformations;
- `security` — deep data-flow/source-to-sink analysis;
- `history` — Git history and ownership;
- `verify` — executable correctness checks.

The deterministic router maps those requests to the current preferred backend. Exact worktree lookup routes to ripgrep, exact fleet lookup to Zoekt, symbol semantics to LSP, architecture/impact to codebase-memory, structural refactors to ast-grep, deep data flow to Joern, and verification to tests. Mutating/high-risk plans include executable verification rather than treating static analysis as proof.

This indirection is intentional: codebase-memory, GitNexus, or another graph engine can be replaced without changing the operation contract used by the rest of Dev Plane.

## Install codebase-memory-mcp

Install the binary and ensure `codebase-memory-mcp` is on `PATH`. Repository MCP configuration expects that command name directly.

On the first session for a repository, index it before depending on graph answers. Keep the graph fresh after meaningful branch changes and re-index if graph output disagrees with compiler/LSP evidence.

For large multi-agent installations, prefer explicit canonical-repository indexing and keep automatic worktree watchers disabled unless every ephemeral worktree should be registered. The bake-off harness indexes canonical repositories explicitly.

## Run shared Zoekt

Zoekt indexes existing local clones; it does not need GitHub credentials in this setup.

```bash
export CODE_REPOS_ROOT="$HOME/src"
docker compose -f docker-compose.code-intelligence.yml --profile index run --rm zoekt-sync
docker compose -f docker-compose.code-intelligence.yml up -d zoekt
```

The web/API endpoint is then available on `http://localhost:6070`.

Re-run `zoekt-sync` after adding/removing repositories or when canonical clones materially change. Agents working in isolated worktrees should use branch-local `rg`, LSP, and `ast-grep` for uncommitted delta context rather than expecting the shared Zoekt index to represent every agent branch.

## Routing policy

1. Exact string, identifier, filename, regex: `rg` locally; Zoekt for fleet-wide search.
2. Structural syntax pattern or mass rewrite: `ast-grep`.
3. Definition/reference/implementation/type resolution: language server.
4. Architecture, dependency, call path, blast radius: `codebase-memory-mcp`.
5. Conceptual discovery when names are unknown: codebase-memory search after cheaper lexical/structural search is insufficient.
6. Security source-to-sink/data-flow question: Joern when installed.
7. Historical reason or ownership: Git.
8. Correctness: tests/typecheck/lint/runtime/CI.

## Multi-agent model

Shared indexes track canonical repository clones, normally `main`. Each coding agent works in an isolated Git worktree with its own language-server state and local structural tools. Treat an agent's effective context as:

```text
canonical shared indexes + branch/worktree delta + executable verification
```

Do not rebuild fleet-wide indexes for every agent worktree.

## Backend bake-off

The benchmark corpus is checked in at `benchmarks/code-intelligence/scenarios.json`. It uses representative production paths from Adacavo, Nulang, Nulang Cloud, and Dev Plane rather than toy symbol lookups. The `expected` arrays are **curated required core paths**, not exhaustive sets of every relevant file.

The current scenarios cover:

- Adacavo proposal lifecycle + durable proposal booking;
- Nulang MIR-to-bytecode code generation + core production callers;
- Nulang Cloud's Dev Plane/local-Wasmtime/Firecracker runtime boundaries;
- Dev Plane scheduler/readiness/budget/executor admission semantics.

Each scenario contains backend-specific MCP calls. Broad codebase-memory discovery uses deterministic BM25 `search_graph(query=...)`, graph caller analysis uses `trace_path`, and every codebase-memory call is explicitly scoped to its indexed project.

The harness:

- optionally refreshes each backend index before measurement;
- uses GitNexus `analyze --index-only` so benchmarking does not rewrite agent instruction/skill files;
- calls both backends through MCP for the query phase;
- records repository-tracked files actually mentioned by each backend response;
- excludes MCP startup and indexing time from query latency while recording `index_ms` separately;
- saves raw backend responses under ignored `data/` for auditability;
- estimates output tokens from response length as a relative cost signal;
- binds final results to exact repository SHAs, backend/tool versions, corpus version, corpus SHA-256, prompts, intents, expected paths, and backend calls.

The corpus contract itself is tested by `scripts/test_codeintel_corpus.py` so stale or unscoped benchmark definitions fail before a live run.

Run the non-live verification lane with:

```bash
bash scripts/verify-codeintel.sh
```

That executes the harness, corpus, and provenance tests plus `packages/repo-intel` Go tests without requiring either graph backend.

To execute the live bake-off using local checkouts:

```bash
export CODEINTEL_RUN_LIVE=1
export CODEINTEL_REPOS_ROOT="$HOME/src"
bash scripts/verify-codeintel.sh
```

For a pinned containerized run:

```bash
export CODEINTEL_REPOS_ROOT="$HOME/src"
docker compose -f docker-compose.code-intelligence-benchmark.yml build benchmark
docker compose -f docker-compose.code-intelligence-benchmark.yml run --rm benchmark
```

The pinned worker installs `codebase-memory-mcp@0.11.0` and `gitnexus@1.6.12`, invokes the installed binaries directly, and isolates persistent state under `/var/lib/codeintel/cbm` and `/var/lib/codeintel/gitnexus`.

Each scenario produces a scorer input in `data/codeintel-bakeoff/<scenario>.json`. Score one with:

```bash
cd packages/repo-intel
go run ./cmd/codeintel-bakeoff ../../data/codeintel-bakeoff/nulang-mir-codegen-callers.json
```

The suite-level promotion verdict is:

```bash
cd packages/repo-intel
go run ./cmd/codeintel-promote ../../data/codeintel-bakeoff/suite.json
```

### Promotion semantics

Required-core recall is the safety metric:

- the candidate must succeed on every scenario;
- the candidate must achieve `1.00` recall on every scenario — every curated core path must be found;
- the candidate must have no per-scenario recall regression versus a successful GitNexus result;
- precision/F1 are retained as diagnostic noise/relevance signals but do not block promotion because the expected set is intentionally non-exhaustive;
- latency and output-token cost are considered only after required-core recall is complete.

`codeintel-bakeoff` therefore ranks successful runs by recall first, then F1, latency, and output tokens. `codeintel-promote` returns a non-zero exit code when promotion is unsafe.

## Woodpecker verification

`.woodpecker/code-intelligence.yaml` provides the normal provider-independent verification lane. It is pinned to the `linux/amd64` agent pool and runs on relevant pull requests and pushes to `main`:

- Python 3.13 harness tests;
- corpus-contract tests;
- provenance tests;
- `packages/repo-intel` Go tests using Go 1.26.8.

The live cross-repository comparison is separate and manual-only in `.woodpecker/code-intelligence-bakeoff.yaml`. It requires the repository to be activated in Woodpecker and a read-only `codeintel_github_token` repository secret for the private Adacavo and Nulang Cloud clones. The token is removed/unset before indexing and backend queries.

See `docs/code-intelligence-bakeoff-runbook.md` for the operational setup and migration sequence.

## CI admission

The code-intelligence lane is intentionally runnable outside GitHub Actions. A hosted Actions run that creates jobs but executes zero steps is an admission/infrastructure failure and must not be treated as evidence that source validation failed. Run the portable or Woodpecker lanes until hosted Actions admission is healthy again.

Do not weaken or remove repository verification because a hosted CI provider is unavailable; move the same checks to a functioning runner.

## Migration from GitNexus

GitNexus remains a temporary fallback where existing repository policy depends on it. Do not delete GitNexus-specific scripts or safety gates until the pinned live corpus returns `promote: true` and a cold-cache repeat confirms the result.

Recommended cutover order:

1. pass the live pinned bake-off;
2. repeat from a cold backend cache;
3. remove the disabled GitNexus fallback from Dev Plane only;
4. bake in codebase-memory as Dev Plane's only graph backend and observe freshness/reliability;
5. rerun the corpus;
6. migrate Adacavo's GitNexus-specific safety/freshness integration in a separate rollback-friendly PR.

Nulang and Nulang Cloud already point at codebase-memory and do not require a GitNexus-removal step from this migration branch.
