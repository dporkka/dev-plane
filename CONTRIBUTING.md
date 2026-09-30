# Contributing

Contributions are welcome. Dev Plane is MIT licensed and should remain useful to teams whose infrastructure and tool choices differ from those of the maintainers.

## Before adding a feature

For changes to reusable infrastructure, answer these questions in the issue or pull request:

1. What provider-neutral problem does this solve?
2. Would the concept still make sense for a team using different model vendors, forges, runtimes, and CI systems?
3. Can the behavior live behind an existing interface or protocol?
4. If a public contract must change, how will existing implementations remain compatible?

Product-specific workflows and vendor-specific configuration generally belong in adapters rather than core domain types.

## Development workflow

Use the repository's checked-in commands and CI configuration as the source of truth.

Typical commands:

```sh
make fmt
make lint
make test
make build
```

For a focused Go module, run that module's own tests while iterating, then run the repository-level gate before considering the change complete. Changes to repository inspection, ecosystem detection, or inferred validation commands should also run `make test-portability`. Changes to forge contracts or adapters should run `make test-forge`. Changes to branch publication, Git/Jujutsu remote handling, or transport authentication should run `make test-publication`.

Behavior changes should be test-driven: add or update the test that describes the desired behavior before changing production logic.

## Provider contributions

A provider implementation should:

- implement a provider-neutral public contract;
- avoid adding vendor-specific fields to generic domain types unless the capability is genuinely universal;
- keep credentials and vendor configuration inside the adapter;
- return the documented common errors where applicable;
- include deterministic tests that require no external account where possible;
- add live/integration tests behind an explicit opt-in flag when external infrastructure is required.

### Runtime providers

Third-party workspace runtimes should run:

```go
contracttest.Run(t, providerFactory, repositoryFixture)
```

from `github.com/ai-dev-control-plane/runtimes/contracttest`.

Provider-specific security properties should have additional tests. Passing the generic contract does not by itself certify isolation.

### Forge providers

Code-forge review adapters should implement `github.com/ai-dev-control-plane/forge.Provider` and run the reusable suite from `github.com/ai-dev-control-plane/forge/contracttest`.

Keep forge API operations separate from Git transport. Opening or merging a review request belongs to the forge provider; publishing a branch with `git push` has different authentication and transport semantics and should not be smuggled into the provider contract.

See [Forge Providers](docs/forge-providers.md) and run `make test-forge` for forge-boundary changes.

### Branch publishers

Branch publication should implement `github.com/ai-dev-control-plane/vcs.Publisher` rather than adding another `git push` subprocess to an application package.

Keep transport authentication generic. Platform-specific conventions such as GitHub's HTTPS username belong in that platform's adapter, while the VCS layer owns generic credential transport and remote/ref semantics.

See [Branch Publication](docs/branch-publication.md) and run `make test-publication` for publication-boundary changes.

## Public contract changes

Treat public provider interfaces, wire protocols, persisted schemas, and repository contracts more carefully than internal packages.

A change to a public contract should include:

- the motivation;
- compatibility impact;
- migration path when required;
- conformance coverage;
- documentation changes.

Prefer additive evolution over forcing every provider to change simultaneously.

## Maintainer integrations

AgentVault, Bifrost, Nulang Cloud, and other systems used by maintainers may have first-party adapters or examples in this repository. They must remain optional unless a capability has been explicitly promoted into provider-neutral Dev Plane core.

Do not use a maintainer integration as the generic type name when the underlying concept has a neutral name.

## Pull requests

Keep pull requests narrow enough to review and verify independently. Include:

- what behavior changes;
- how it was tested;
- any public-contract or migration impact;
- any external service required to exercise optional integration tests.

Documentation-only and configuration-only changes do not need artificial tests, but behavioral changes do.
