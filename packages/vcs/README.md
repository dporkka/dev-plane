# Agent VCS

A small machine-facing source-control layer for Dev Plane.

## Goals

- Give every task/agent an isolated workspace.
- Keep raw Git/Jujutsu shell commands out of model-visible tools.
- Support Git today and Jujutsu (`jj`) as the agent-oriented backend.
- Preserve Git forge compatibility.
- Persist provenance separately from commit messages.
- Keep credentials out of command arguments and repository URLs.

## Architecture

```text
worker / orchestrator
        |
        v
   vcs.Manager
    /      \
 GitBackend JujutsuBackend
    \      /
   CommandRunner
        |
 repository + isolated task workspaces
        |
 provenance Recorder -> JSONL now; AgentVault/NATS/Dolt later
```

`JujutsuBackend` uses colocated JJ/Git repositories. A task workspace is created
with an explicit base revision and has its own working-copy commit. Publishing
moves a task bookmark to the completed change and pushes that bookmark.

## Suggested defaults

For Jujutsu, pass an immutable/explicit base such as `main@origin` after fetch.
For Git, pass the corresponding Git ref such as `origin/main`.

Never put PATs or installation tokens in `CloneRequest.URL`. Configure Git/JJ
credential helpers or `GIT_ASKPASS` through `CloneRequest.Env` at the runtime
boundary.

## Next integration step

The module is registered in the root `go.work`. After GitNexus impact analysis
is available, wire it into the existing `repo-intel`, runtime, reviewer, and PR
factory paths. The migration should replace mutating ad-hoc Git subprocess
calls first while allowing read-only Git consumers to continue operating against
colocated JJ/Git workspaces.
