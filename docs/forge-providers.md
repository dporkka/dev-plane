# Forge Providers

Dev Plane uses the public `forge.Provider` contract for provider-neutral code-review change requests.

The contract intentionally models the shared semantics of GitHub pull requests, GitLab merge requests, Gitea pull requests, and similar forge review objects without adopting one vendor's API vocabulary.

## Scope

The initial stable contract covers two operations:

```text
OpenChange
MergeChange
```

It does **not** currently cover:

- OAuth login or account discovery;
- repository listing/discovery;
- webhook registration or delivery;
- deployment APIs;
- issue tracking;
- branch publication / `git push` transport.

Those concerns have different portability and security requirements and should not be forced into this interface merely because one vendor exposes them through the same API.

Branch publication now has its own public `vcs.Publisher` boundary. Git-over-HTTPS authentication remains separate from forge API authentication; see [Branch Publication](branch-publication.md).

## Provider interface

```go
type Provider interface {
    Name() string

    OpenChange(
        ctx context.Context,
        credential Credential,
        repository Repository,
        req OpenChangeRequest,
    ) (*Change, error)

    MergeChange(
        ctx context.Context,
        credential Credential,
        repository Repository,
        number int,
        req MergeChangeRequest,
    ) (*MergeResult, error)
}
```

The generic repository identifier uses `Namespace`, not `Owner`:

```go
forge.Repository{
    Namespace: "acme/platform",
    Name:      "widget",
}
```

A GitHub adapter maps namespace to owner. A GitLab adapter may use a nested group namespace.

## Authentication

`forge.Credential` currently carries an optional token, but Dev Plane callers do not require it to be non-empty.

Authentication policy belongs to the provider. This supports providers that:

- use a passed access token;
- own credentials in their own configuration;
- use workload identity or another ambient mechanism;
- operate against a local test forge.

Providers should avoid putting credentials into URLs, logs, error strings, or persisted change metadata.

## Change semantics

`OpenChangeRequest` contains the portable review concepts:

- title/body;
- source branch (`Head`);
- target branch (`Base`);
- draft state.

`MergeChangeRequest` supports the common merge methods:

- `merge`;
- `squash`;
- `rebase`.

An empty method normalizes to `merge`.

The optional `ExpectedHeadRevision` allows adapters to use optimistic concurrency when the upstream forge supports it.

## Common errors

The contract defines provider-neutral error categories:

- `forge.ErrInvalidRequest`;
- `forge.ErrNotFound`;
- `forge.ErrConflict`.

All providers must reject invalid provider-neutral input consistently. Upstream API error normalization can be expanded without changing the core interface.

## Conformance tests

Third-party providers should run the reusable suite from:

```text
github.com/ai-dev-control-plane/forge/contracttest
```

Example:

```go
func TestMyForgeContract(t *testing.T) {
    contracttest.Run(t, func(t *testing.T) contracttest.Fixture {
        return contracttest.Fixture{
            Provider:   newProviderForTest(t),
            Credential: forge.Credential{Token: testToken(t)},
            Repository: forge.Repository{
                Namespace: testNamespace(t),
                Name:      testRepository(t),
            },
        }
    })
}
```

The suite verifies:

- a non-empty provider identity;
- opening a draft review change;
- portable branch metadata;
- a positive change number and browseable URL;
- merging with a common merge strategy;
- a resulting merged revision;
- consistent rejection of invalid open/merge inputs.

Provider-specific behavior should have additional tests.

## Built-in implementations

### Deterministic memory provider

`packages/forge/forgetest` provides a credential-free in-memory implementation for application tests and examples.

It exists to make the neutral contract independently executable; it is not a production forge.

### GitHub

`gateway.GitHubGateway` implements `forge.Provider` while retaining its existing GitHub-specific OAuth, repository, webhook, deployment, and legacy PR methods.

The adapter translates:

```text
forge.Repository.Namespace -> GitHub owner
OpenChange                 -> CreatePR
MergeChange                -> MergePR
Credential.Token           -> OAuth bearer token
```

The GitHub implementation runs the same forge conformance contract through a local `httptest` server, so the contract does not require a live GitHub account.

### Gitea / Forgejo

`gateway.GiteaGateway` implements the same `forge.Provider` contract against the Gitea-compatible API v1 surface used by Gitea and Forgejo deployments.

The adapter translates:

```text
forge.Repository.Namespace -> Gitea owner / organization
OpenChange                 -> POST /api/v1/repos/{owner}/{repo}/pulls
MergeChange                -> POST /api/v1/repos/{owner}/{repo}/pulls/{index}/merge
ExpectedHeadRevision       -> head_commit_id
Credential.Token           -> Authorization: token <token>
```

Gitea-compatible pull-request creation does not expose a portable draft input field. The adapter therefore maps `Draft=true` to the instance's configured work-in-progress title prefix, defaulting to `WIP:`. Configure a different prefix with `GITEA_DRAFT_TITLE_PREFIX` when the instance has customized that setting.

The merge endpoint returns an empty success response, so the adapter reads the pull request after merge to obtain the authoritative merged state and merge revision.

Gitea repository ownership is one owner or organization segment. A nested neutral namespace such as `group/subgroup` is therefore rejected by this adapter rather than incorrectly flattening it. Other providers, such as a future GitLab adapter, may support nested namespaces.

Configure the application with:

```sh
FORGE_PROVIDER=gitea        # or forgejo
GITEA_URL=https://code.example.com
GITEA_TOKEN=...
GITEA_USERNAME=alice        # needed for Dev Plane-managed HTTPS git push
FORGE_GIT_REMOTE=origin
```

`gateway.NewGiteaBranchPublisher` uses the username plus access token as the HTTPS Git credential while keeping that transport authentication separate from the forge API contract.

The Gitea adapter runs `forge/contracttest` plus deterministic HTTP tests for draft translation, optimistic merge revision checks, provider error normalization, and HTTPS publication credential mapping.

## Adding another forge

A new adapter should normally require no changes to `packages/forge`.

Before changing the contract to accommodate a provider, determine whether the requested concept is genuinely shared by multiple forges. Provider-only metadata belongs in the adapter.

A proposed additional forge implementation should:

1. implement `forge.Provider`;
2. pass `forge/contracttest`;
3. keep provider-specific authentication/configuration outside core types;
4. add deterministic HTTP adapter tests;
5. place credential-dependent live tests behind an explicit opt-in flag.

Run:

```sh
make test-forge
```

before submitting forge-boundary changes.
