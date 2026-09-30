# Branch Publication

Dev Plane separates **publishing source-control refs** from **opening or merging forge review changes**.

These are different operations:

```text
local workspace
    |
    v
vcs.Publisher
    |
    v
remote branch / bookmark
    |
    v
forge.Provider
    |
    v
pull request / merge request / review change
```

The separation keeps Git transport authentication out of the forge API contract and lets repositories use Git or Jujutsu publication independently of GitHub, GitLab, Gitea, or another review forge.

## Public contract

The publication boundary is:

```go
type Publisher interface {
    Publish(ctx context.Context, req PublishRequest) error
}

type PublishRequest struct {
    WorkspacePath string
    Ref           string
    Remote        string
    Env           map[string]string
}
```

`Remote` defaults to `origin` when omitted.

The built-in `GitBackend` and `JujutsuBackend` both implement this contract.

## Authentication

Credentials must not be embedded in command arguments or repository URLs.

For HTTPS remotes, `vcs.NewHTTPBasicPublisher` wraps another publisher and creates an ephemeral `GIT_ASKPASS` helper for each publication attempt.

The helper reads generic environment variables:

```text
DEV_PLANE_GIT_USERNAME
DEV_PLANE_GIT_PASSWORD
```

The credential values are not written into the helper script. The temporary helper directory is deleted when publication completes.

Other transports can provide their own environment through `PublishRequest.Env`, for example SSH configuration, credential helpers, or workload-specific Git configuration.

## GitHub

GitHub's HTTPS token convention is an adapter concern.

`gateway.NewGitHubBranchPublisher` maps a GitHub token to generic HTTP basic publication using GitHub's `x-access-token` username.

That string exists in the GitHub adapter, not in `prfactory` or the public VCS contract.

## PR factory

`prfactory.Factory` depends only on `vcs.Publisher` for branch publication:

```go
factory.
    WithBranchPublisher(publisher).
    WithBranchRemote("origin")
```

The factory no longer invokes `git push` directly and no longer creates GitHub-specific askpass scripts.

The API composition root wires the branch publisher and forge provider together so HTTP review creation uses the same configured integration boundary as merge.

## Repository identity

Forge repository identities use:

```go
forge.Repository{
    Namespace: "group/subgroup",
    Name:      "project",
}
```

`forge.ParseRepositoryFullName` splits on the final slash, so both `owner/repo` and nested `group/subgroup/repo` forms are valid.

Opening and merging review changes use this same parser.

## Verification

The publication contract includes a network-free integration test that:

1. creates a temporary bare Git repository;
2. creates and commits a local feature branch;
3. publishes through `vcs.GitBackend` to a non-default remote name;
4. verifies the remote branch points at exactly the local commit.

Run:

```sh
make test-publication
```

No hosted forge account is required for the core publication tests.

## Adding another transport

A transport should normally implement `vcs.Publisher` without changing `prfactory`.

Examples include:

- Git over SSH;
- Git with a platform-specific credential helper;
- Jujutsu bookmark publication;
- a sandbox/runtime-mediated publisher.

Keep forge review APIs in `forge.Provider` and transport credentials in the VCS publication layer. Do not combine them merely because one vendor supplies both services.
