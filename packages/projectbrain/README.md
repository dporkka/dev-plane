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

## GitNexus adapter

`GitNexusSource` converts code-graph observations into ordinary Project Brain facts through a small transport-neutral `GitNexusClient` interface. The transport can be an MCP bridge, local sidecar, or another trusted host adapter; the compiler does not depend on GitNexus protocol details.

The adapter binds graph data to `git-commit:<sha>` identities and compares the requested commit with the GitNexus snapshot's `lastCommit` before returning any facts. A stale index therefore contributes no stale facts. Through the compiler, source failure/staleness is surfaced as a warning while other independent sources can still contribute context.

Current mappings are deliberately small:

```text
GitNexus symbol        -> symbol:<name> defined_in <file>:<line>
GitNexus relationship  -> symbol:<from> <relation> symbol:<to>
GitNexus process step  -> process:<name> step <position>:symbol:<name>@<location>
```

## Agent-run integration

The API runner exposes one optional `ProjectContextProvider` seam. `*projectbrain.Compiler` implements it directly.

When configured, the runner:

1. resolves the workspace's immutable `HEAD` commit;
2. derives the context request from the task objective, acceptance criteria, and declared files to change/create;
3. compiles Project Brain context for that exact commit;
4. validates the returned package identity and digest;
5. persists the complete context package in existing `agent_runs.metadata` before the first model call; and
6. injects selected facts into the system prompt as JSON Lines explicitly framed as untrusted repository observations.

No schema migration is required. Existing metadata such as run-manifest authority is preserved during the merge. When no context provider is configured, runner behavior is unchanged.

Project Brain uses the committed source revision for source-code intelligence. Dev Plane's separate verification/capability authority continues to use its working-tree `git-tree:<sha>` identity where dirty and untracked content must be represented.

## Source architecture

```text
GitNexus graph ─┐
LSP / AST ──────┤
Git history ────┤
Tests / schema ─┼─> projectbrain.Source -> Compiler -> ContextPackage
Runtime traces ─┤                                  |
Deployments ────┤                                  v
ADRs / docs ────┘                         durable AgentRun metadata
                                                     |
                                                     v
                                             bounded agent prompt
```

Embeddings may help a source retrieve candidate facts, but they are not treated as authoritative project state. Source control, language tooling, schemas, executed verification, and runtime evidence should remain the truth-bearing inputs.

## Activation boundary

This package intentionally does not make the API process spawn `npx gitnexus` or depend on a local developer MCP configuration. Production activation should inject a trusted `GitNexusClient` at the service composition boundary, then configure the runner with `NewCompiler(NewGitNexusSource(client, ...))`.

Keeping transport outside the compiler avoids making Node/npm availability, local MCP config, or model-visible credentials part of the Dev Plane runtime contract.

## Next integration

1. Implement the production GitNexus `GitNexusClient` bridge at the composition boundary and configure the runner with `NewCompiler(NewGitNexusSource(...))`.
2. Add a second independent source (LSP/AST or repository/test facts) so GitNexus degradation does not reduce context to zero.
3. Record context digest/model/context strategy alongside verification outcomes for the evaluation flywheel.
4. Add runtime/deployment facts only after the source-code context path has production evidence.
