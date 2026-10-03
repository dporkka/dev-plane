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

The benchmark corpus is checked in at `benchmarks/code-intelligence/scenarios.json`. It starts with representative high-value scenarios from Adacavo, Nulang, Nulang Cloud, and Dev Plane rather than toy symbol lookups.

For each scenario, run each candidate backend against the same prompt and record normalized file/symbol keys, elapsed time, output-token count, and any error/stale-index condition. Feed those observations to the scorer. Input format:

```json
{
  "scenario": "nulang-mir-codegen-callers",
  "expected": ["src/mir_codegen.rs", "src/main.rs"],
  "runs": [
    {
      "backend": "codebase-memory",
      "results": ["src/mir_codegen.rs", "src/main.rs"],
      "latency_ms": 180,
      "output_tokens": 850
    },
    {
      "backend": "gitnexus",
      "results": ["src/mir_codegen.rs"],
      "latency_ms": 120,
      "output_tokens": 600
    }
  ]
}
```

Score it with:

```bash
cd packages/repo-intel
go run ./cmd/codeintel-bakeoff /path/to/results.json
```

Ranking policy is intentionally conservative: failed runs sort last; otherwise retrieval F1 wins first, then latency, then output-token count. A fast backend that misses affected code must not beat a slower correct backend.

Run package tests with:

```bash
cd packages/repo-intel
go test ./...
```

## Migration from GitNexus

GitNexus remains a temporary fallback where existing repository policy depends on it. Do not delete GitNexus-specific scripts or safety gates until codebase-memory has been benchmarked on equivalent architecture, impact, change-detection, and cross-repository tasks.

Removal criteria:

- equivalent or better call-path/impact accuracy on representative changes;
- reliable incremental freshness;
- no regression in pre-commit affected-scope checks;
- lower or comparable latency/token overhead after quality is equivalent;
- stable behavior across TypeScript, Go, Rust, and Python repositories.
