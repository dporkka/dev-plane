# Code Intelligence Stack

Dev Plane uses a layered code-intelligence model so agents use the cheapest precise signal first and reserve graph analysis for questions that need it.

## Ownership

Shared services:

- `codebase-memory-mcp`: persistent structural graph, architecture, call paths, impact analysis, cross-repository relationships, semantic fallback.
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

The deterministic router maps those requests to the current preferred backend. For example, exact worktree lookup routes to ripgrep, exact fleet lookup to Zoekt, symbol semantics to LSP, architecture/impact to codebase-memory, structural refactors to ast-grep, deep data flow to Joern, and verification to tests. Mutating/high-risk plans include an executable verification step rather than treating static analysis as proof.

This indirection is intentional: codebase-memory, GitNexus, or another graph engine can be replaced without changing the operation contract used by the rest of Dev Plane.

## Install codebase-memory-mcp

Install the binary from the upstream project and ensure `codebase-memory-mcp` is on `PATH`. Repository MCP configuration expects that command name directly.

On the first session for a repository, index it before depending on graph answers. Keep the graph fresh after meaningful branch changes and re-index if graph output disagrees with compiler/LSP evidence.

For large multi-agent installations, prefer explicit canonical-repository indexing and keep automatic worktree watchers disabled unless you actually want every ephemeral worktree registered. The bake-off harness indexes explicitly and does not require auto-watch.

## Run shared Zoekt

Zoekt indexes existing local clones; it does not need GitHub credentials in this setup.

```bash
export CODE_REPOS_ROOT="$HOME/src"
docker compose -f docker-compose.code-intelligence.yml --profile index run --rm zoekt-sync
docker compose -f docker-compose.code-intelligence.yml up -d zoekt
```

The web/API endpoint is then available on `http://localhost:6070`.

Re-run `zoekt-sync` after adding/removing repositories or when the canonical clones have materially changed. Agents working in isolated worktrees should use branch-local `rg`, LSP, and `ast-grep` for uncommitted delta context rather than expecting the shared Zoekt index to represent every agent branch.

## Routing policy

1. Exact string, identifier, filename, regex: `rg` locally; Zoekt for fleet-wide search.
2. Structural syntax pattern or mass rewrite: `ast-grep`.
3. Definition/reference/implementation/type resolution: language server.
4. Architecture, dependency, call path, blast radius: `codebase-memory-mcp`.
5. Conceptual search with unknown names: semantic graph search as a fallback.
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

The benchmark corpus is checked in at `benchmarks/code-intelligence/scenarios.json`. It starts with representative high-value scenarios from Adacavo, Nulang, Nulang Cloud, and Dev Plane rather than toy symbol lookups. Each scenario contains backend-specific MCP tool calls so the comparison is executable and reproducible.

The harness:

- optionally refreshes each backend index before measurement;
- uses GitNexus `analyze --index-only` so benchmarking does not rewrite agent instruction/skill files;
- calls both backends through MCP for the query phase;
- records only repository-tracked files actually mentioned by each backend response;
- excludes MCP startup and indexing time from query latency, while recording `index_ms` separately;
- saves raw backend responses under the ignored `data/` directory for auditability;
- estimates output tokens from response length for a stable relative cost signal.

Run the non-live verification lane with:

```bash
bash scripts/verify-codeintel.sh
```

That executes the Python harness unit tests and `packages/repo-intel` Go tests without requiring either graph backend.

To execute the live bake-off when the repositories are checked out under one root:

```bash
export CODEINTEL_RUN_LIVE=1
export CODEINTEL_REPOS_ROOT="$HOME/src"
bash scripts/verify-codeintel.sh
```

By default the harness expects checkouts named `adacavo`, `nulang`, `nulang-cloud`, and `dev-plane` under `CODEINTEL_REPOS_ROOT`. Override any checkout explicitly when needed:

```bash
python3 scripts/codeintel_bakeoff.py \
  --repo dporkka/adacavo=/work/adacavo \
  --repo nulang-org/nulang=/work/nulang
```

The default backend commands are:

```text
codebase-memory: codebase-memory-mcp
gitnexus MCP:    npx -y gitnexus@1.6.12 mcp
gitnexus index:  npx -y gitnexus@1.6.12 analyze --index-only
```

They can be overridden through `CODEINTEL_CODEBASE_MEMORY_CMD`, `CODEINTEL_GITNEXUS_MCP_CMD`, and `CODEINTEL_GITNEXUS_ANALYZE_CMD` so a runner can use preinstalled or pinned binaries without changing the corpus.

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

The default promotion gate requires the candidate backend to succeed on every scenario, achieve at least 0.80 recall and 0.75 F1 on every scenario, and have no per-scenario recall or F1 regression versus a successful GitNexus baseline. Latency and output-token cost never compensate for lower retrieval quality.

## Woodpecker verification

`.woodpecker/code-intelligence.yaml` provides a provider-independent verification lane for this subsystem. It runs on relevant pull-request changes and pushes to `main` and executes:

- the Python 3.13 harness tests;
- the `packages/repo-intel` Go tests using Go 1.26.8.

Woodpecker only runs repository workflows after the repository is activated in the Woodpecker server and its forge webhook is installed. Repository activation is an infrastructure/admin operation, not something encoded in this repository.

The live cross-repository bake-off is intentionally not part of the default PR lane because it needs canonical checkouts of four repositories plus both backend binaries. Run it on a trusted benchmark worker with those dependencies preinstalled or mounted.

## CI admission

The code-intelligence lane is intentionally runnable outside GitHub Actions. A hosted Actions run that creates jobs but executes zero steps is an admission/infrastructure failure and must not be treated as evidence that source validation failed. Run `bash scripts/verify-codeintel.sh` on an available local, Woodpecker, Dagger, or other repository-defined runner until hosted Actions admission is healthy again.

Do not weaken or remove repository verification because a hosted CI provider is unavailable; move the same commands to a functioning runner.

## Migration from GitNexus

GitNexus remains a temporary fallback where existing repository policy depends on it. Do not delete GitNexus-specific scripts or safety gates until codebase-memory has been benchmarked on equivalent architecture, impact, change-detection, and cross-repository tasks.

Removal criteria:

- equivalent or better call-path/impact accuracy on representative changes;
- reliable incremental freshness;
- no regression in pre-commit affected-scope checks;
- lower or comparable latency/token overhead after quality is equivalent;
- stable behavior across TypeScript, Go, Rust, and Python repositories.
