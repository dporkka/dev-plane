# Code Intelligence

Use `codebase-memory-mcp` as the primary structural code-intelligence backend for Dev Plane. On a fresh checkout, index the repository before relying on graph results. Prefer the cheapest precise tool for each question instead of routing every task through the graph.

## Tool routing

- Exact identifier, string, filename, or regex lookup: use `rg` or Zoekt when available.
- Structural syntax pattern or repeatable codemod: use `ast-grep`.
- Definition, reference, implementation, rename, or type-resolution questions: prefer the language server / SCIP-quality semantic tooling.
- Architecture, dependency, call-path, cross-file impact, or blast-radius questions: use `codebase-memory-mcp` (`get_architecture`, `search_graph`, `trace_path`, `detect_changes`, and `query_graph` as appropriate).
- Conceptual search when names are unknown: use semantic graph search only after cheaper lexical/structural search is insufficient.
- Security/data-flow analysis requiring source-to-sink reasoning: use Joern when available; do not use it for routine navigation.
- Historical intent or ownership: use Git history.
- Correctness: verify with the repository's tests, type checks, linters, and CI; static code intelligence is not proof that a change works.

## Change safety

Before changing a high-fan-in symbol, public API, persistence contract, scheduler path, capability boundary, or execution protocol, inspect upstream/downstream impact with the graph and semantic tooling. Before committing a non-trivial change, run `detect_changes` against the working tree or `main` and confirm the affected scope matches the intended change.

If graph results conflict with compiler/LSP output or executable tests, treat compiler/LSP and runtime evidence as authoritative and refresh/re-index the graph.

## Migration note

GitNexus remains a temporary fallback during the bake-off but is not the default code-intelligence path. Do not add new GitNexus-specific workflow dependencies unless a measured gap in codebase-memory requires it.
