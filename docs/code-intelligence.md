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

## Migration from GitNexus

GitNexus remains a temporary fallback where existing repository policy depends on it. Do not delete GitNexus-specific scripts or safety gates until codebase-memory has been benchmarked on equivalent architecture, impact, change-detection, and cross-repository tasks.

Removal criteria:

- equivalent or better call-path/impact accuracy on representative changes;
- reliable incremental freshness;
- no regression in pre-commit affected-scope checks;
- lower or comparable latency/token overhead;
- stable behavior across TypeScript, Go, Rust, and Python repositories.
