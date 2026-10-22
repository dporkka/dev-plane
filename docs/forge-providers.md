# Forge providers

Dev Plane treats the Git forge as an external collaboration and source-control authority, not as the agent execution plane.

The intended architecture is:

```text
GitHub / Gitea
      |
      | repositories, issues, pull requests, webhooks, checks
      v
Dev Plane
      |
      | goals, work items, agents, policy, verification, evidence
      v
Workspace provider
      |
      v
local / OCI / Nulang Cloud
```

## Authority boundary

The forge owns:

- Git repository hosting;
- repository membership and ordinary forge permissions;
- issues and pull requests;
- releases and packages;
- forge webhooks and status/check presentation.

Dev Plane owns:

- Goals and WorkItems;
- AgentRuns and model routing;
- repository context and Project Brain;
- policy and approvals;
- change orchestration;
- verification and immutable evidence;
- merge/deployment intent and audit provenance.

Workspace providers own isolated command/file execution. Dev Plane must not depend on a particular sandbox implementation in order to support a forge.

## `gateway.Forge`

The first provider-neutral contract intentionally contains only semantics required by current Dev Plane workflows:

- repository discovery;
- pull-request creation;
- pull-request merge with optional exact-head binding;
- repository webhook creation/deletion.

Provider-specific capabilities should not be added to this interface merely because an API exposes them. Add a cross-forge semantic only when a Dev Plane workflow actually requires it.

## GitHub

`GitHubForge` adapts the existing `GitHubGateway`. Existing GitHub-specific callers remain supported during migration.

## Gitea

`GiteaForge` uses Gitea's `/api/v1` API and supports self-hosted instance URLs, including installations hosted under a URL subpath.

The v1 provider currently supports:

- token-authenticated repository list/get;
- pull-request creation;
- merge, squash, and rebase;
- exact reviewed-head binding through `head_commit_id`;
- webhook create/delete.

### Draft pull requests

Gitea 1.25's create-pull-request request schema does not expose a draft input even though pull-request responses expose draft state. Dev Plane therefore returns `ErrUnsupportedForgeCapability` when a caller requests `Draft=true` through this provider.

This is deliberate. The provider must not silently weaken a requested draft into a normal pull request or encode semantics through title conventions.

## Credentials

`ForgeCredential` currently contains an API access token. Treat this as a forge API credential only.

Git transport credentials are a separate concern. In particular, GitHub's `x-access-token` HTTPS convention must not become the generic Gitea credential model. The PR Factory migration should introduce a separate Git push credential/transport boundary before enabling end-to-end Gitea branch publication.

## Migration sequence

1. Qualify the `gateway.Forge` contract and both adapters on the repository-native Go toolchain.
2. Migrate PR Factory from its private GitHub creator interface to `gateway.Forge`.
3. Separate Git push credentials from forge API credentials.
4. Add repository-level forge provider/base-URL authority.
5. Route webhook ingestion through provider-aware verification.
6. Normalize commit/check status publication so verified evidence can be surfaced on both GitHub and Gitea.
7. Dogfood Dev Plane against an unmodified Gitea deployment before adding any custom forge UI.

## Non-goals

Do not fork Gitea to add Dev Plane features unless an unavoidable forge-level primitive is missing. Agent orchestration, workspace execution, Project Brain, verification, proof, and policy belong in Dev Plane or its workspace providers, not inside the forge.
