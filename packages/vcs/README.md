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
credential helpers or `GIT_ASKPASS` through environment-based transport
configuration.

## Branch publication

`Publisher` is the narrow public boundary for publishing a workspace ref.
`GitBackend` and `JujutsuBackend` both implement it, and callers may select a
remote explicitly through `PublishRequest.Remote` (default: `origin`).

`NewHTTPBasicPublisher` adds ephemeral HTTPS authentication without placing
credentials in command arguments, repository URLs, or generated scripts.

The PR factory now consumes `vcs.Publisher` directly. Platform-specific
credential conventions are adapters layered on top; for example, the GitHub
gateway maps a token to GitHub's HTTPS username convention.

See [Branch Publication](../../docs/branch-publication.md) and run
`make test-publication` for focused verification.

## Remaining integration work

The VCS module is registered in the root `go.work` and now owns PR-factory
branch publication. Read-only Git consumers and remaining mutation paths can be
migrated incrementally without forcing the repository into one VCS backend.
