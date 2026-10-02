# Project Brain

`projectbrain` defines the revision-bound knowledge and context-compilation contract for Dev Plane agents.

The package is intentionally source-agnostic. First-party repository intelligence, GitNexus, LSP/AST analysis, repository metadata, tests, schemas, deployment telemetry, and other intelligence providers implement the `Source` interface and return normalized facts with provenance.

## Invariants

- Agent context is compiled for an explicit repository + immutable revision.
- Revision-scoped facts from any other revision are excluded.
- Repository-scoped facts may be reused across revisions when the source explicitly marks them that way.
- Equivalent facts from multiple sources are merged while preserving provenance.
- Task relevance is ranked before confidence so a very confident unrelated fact does not crowd out relevant evidence.
- Context size is bounded deterministically.
- Source failures are visible as warnings rather than silently degrading context.
- The context package carries a stable SHA-256 digest suitable for persisting on an agent run. Observation timestamps are deliberately excluded from the digest so re-observing unchanged knowledge does not create a different context identity.

## First-party repository source

The production-safe baseline source adapts Dev Plane's existing `packages/repo-intel` lexical indexer. It supports the languages already understood by that package and emits revision-scoped symbol-definition facts.

Before it emits any fact, the adapter verifies both:

1. workspace `HEAD` exactly matches the requested `git-commit:<sha>`; and
2. a temporary-index tree of the complete workspace matches `HEAD^{tree}`.

The second check includes tracked modifications, deletions, and non-ignored untracked files without mutating the real Git index. A dirty or mismatched checkout therefore contributes no falsely revision-bound facts; the compiler records the source failure as a warning and may still use other sources.

Filesystem reads are separately confined to the workspace. The shared lexical indexer skips source-file symlinks, and task-declared changed paths reject traversal, absolute paths, direct symlinks, and paths whose resolved target leaves the workspace. A clean Git tree therefore cannot use a tracked symlink as a host-file ingestion path.

The worker rollout is opt-in:

```text
--project-brain
PROJECT_BRAIN_ENABLED=true
```

When enabled this activates only the first-party `repo-intel` source. It does not spawn or require an external code-graph service.

## GitNexus adapter

`GitNexusSource` converts code-graph observations into ordinary Project Brain facts through a small transport-neutral `GitNexusClient` interface. The transport can be an MCP bridge, local sidecar, or another trusted host adapter; the compiler does not depend on GitNexus protocol details.

The adapter binds graph data to `git-commit:<sha>` identities and compares the requested commit with the GitNexus snapshot's `lastCommit` before returning any facts. A stale index therefore contributes no stale facts. Through the compiler, source failure/staleness is surfaced as a warning while other independent sources can still contribute context.

Current mappings are deliberately small:

```text
GitNexus symbol        -> symbol:<name> defined_in <file>:<line>
GitNexus relationship  -> symbol:<from> <relation> symbol:<to>
GitNexus process step  -> process:<name> step <position>:symbol:<name>@<location>
```

GitNexus is not a required Dev Plane runtime dependency. Its upstream project currently uses the PolyForm Noncommercial 1.0.0 license, so commercial deployments must verify that their use is permitted or obtain appropriate terms. `agentexecutor.WithGitNexusClient` accepts a caller-supplied client and composes it with the first-party source; Dev Plane does not construct, distribute, or launch GitNexus itself.

## Agent-run integration

The API runner exposes one optional `ProjectContextProvider` seam. `*projectbrain.Compiler` implements it directly, and `WorkspaceContextCompiler` extends the seam for sources that require the concrete workspace path.

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
repo-intel ──────┐
GitNexus* ───────┤
LSP / AST ───────┤
Git history ─────┤
Tests / schema ──┼─> projectbrain.Source -> Compiler -> ContextPackage
Runtime traces ──┤                                  |
Deployments ─────┤                                  v
ADRs / docs ─────┘                         durable AgentRun metadata
                                                     |
                                                     v
                                             bounded agent prompt

* optional external source
```

Embeddings may help a source retrieve candidate facts, but they are not treated as authoritative project state. Source control, language tooling, schemas, executed verification, and runtime evidence should remain the truth-bearing inputs.

## Activation boundary

`agentexecutor.EnableProjectBrain` composes the first-party workspace source. The worker exposes that through `--project-brain` / `PROJECT_BRAIN_ENABLED` and leaves it disabled by default for controlled rollout.

`agentexecutor.WithGitNexusClient` is an explicit integration boundary for deployments that independently provide a compatible GitNexus client. The API process never spawns `npx gitnexus`, depends on a local developer MCP configuration, or makes GitNexus availability part of Dev Plane's core runtime contract.

Keeping external transport outside the compiler avoids making Node/npm availability, local MCP config, model-visible credentials, or non-permissive dependencies part of the control plane.

## Evaluation boundary

The complete context package and digest are persisted on `agent_runs.metadata`, while existing `model_usage` rows already reference `agent_run_id`. This gives Dev Plane a joinable basis for comparing model, cost, context strategy, and later verification outcomes without creating another telemetry schema in this slice.

## Verification boundary

The original Project Brain package and GitNexus adapter were developed test-first and verified in the available isolated Go environment. Later workspace-source and rollout changes are also test-first, but full repository verification still requires the checked-in Go toolchain and authoritative CI/runtime lane. If those lanes fail before executing steps, that is not treated as evidence for or against this code.

## Next integration

1. Run the exact PR head through an authoritative Go/CI lane, then enable Project Brain for a small local-workspace cohort.
2. Add a stronger first-party semantic source (LSP/tree-sitter) behind the same `Source` contract rather than making an external graph mandatory.
3. Join run context identity with model usage and verification outcomes to learn routing/context policy empirically.
4. Add runtime/deployment facts only after the source-code context path has production evidence.
