# Project Brain

`projectbrain` defines the revision-bound knowledge and context-compilation contract for Dev Plane agents.

The package is intentionally source-agnostic. GitNexus, LSP/AST analysis, repository metadata, tests, schemas, deployment telemetry, and other intelligence providers implement the `Source` interface and return normalized facts with provenance.

## Invariants

- Agent context is compiled for an explicit repository + immutable revision.
- Revision-scoped facts from any other revision are excluded.
- Repository-scoped facts may be reused across revisions when the source explicitly marks them that way.
- Equivalent facts from multiple sources are merged while preserving provenance.
- Task relevance is ranked before confidence so a very confident unrelated fact does not crowd out relevant evidence.
- Context size is bounded deterministically.
- Source failures are visible as warnings rather than silently degrading context.
- The context package carries a stable SHA-256 digest suitable for persisting on an agent run. Observation timestamps are deliberately excluded from the digest so re-observing unchanged knowledge does not create a different context identity.

## Intended source adapters

```text
GitNexus graph ─┐
LSP / AST ──────┤
Git history ────┤
Tests / schema ─┼─> projectbrain.Source -> Compiler -> ContextPackage
Runtime traces ─┤
Deployments ────┤
ADRs / docs ────┘
```

Embeddings may help a source retrieve candidate facts, but they are not treated as authoritative project state. Source control, language tooling, schemas, executed verification, and runtime evidence should remain the truth-bearing inputs.

## Next integration

1. Add a GitNexus source adapter that emits symbol/relationship/execution-flow facts.
2. Persist compiled package digest + source provenance on the agent run/task capsule.
3. Add the selected facts to agent prompts through a single optional context-provider seam.
4. Add runtime/deployment facts after the source-code path is proven.
