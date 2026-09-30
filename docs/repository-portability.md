# Repository Portability Contract

Dev Plane should be useful for ordinary software repositories that have no relationship to the projects maintained by Dev Plane's authors.

The portability contract is a small executable suite under:

```text
apps/api/internal/tools/testdata/portable-repos/
├── go-service/
├── typescript-package/
├── rust-cli/
└── python-package/
```

The fixtures intentionally contain **no Dev Plane configuration** and no references to AgentVault, Bifrost, Nulang, or other maintainer projects.

## What the contract proves

For each fixture, Dev Plane must be able to inspect the repository and determine at least:

- the primary source language is recognized;
- the conventional package/tool manager is recognized;
- the repository-native test command is inferred;
- the ecosystem manifest is surfaced as a key file.

The suite also verifies deterministic precedence when generic manifests coexist with more specific lockfiles. Test-command inference is deliberately conservative: a JavaScript manifest must declare a `test` script, and Python must expose a pytest signal such as pytest configuration or dependency metadata. A bare manifest is never treated as proof that tests exist. For example:

- `pnpm-lock.yaml` wins over `package.json`;
- `yarn.lock` wins over `package.json`;
- `uv.lock` wins over a generic `pyproject.toml`;
- `poetry.lock` wins over a generic `pyproject.toml`;
- a lone `pyproject.toml` means generic Python, not Poetry, and does not by itself imply pytest.

## Run it

```sh
make test-portability
```

The normal API/full repository test suites also include these tests.

## Adding an ecosystem

A new fixture should look like an ordinary minimal project copied from that ecosystem's conventions. Do not add `dev-plane.json`, maintainer-specific services, or credentials merely to make inspection succeed.

Prefer dependency-free fixtures where practical so inspection remains deterministic and does not require network access.

Add the fixture to `TestPortableRepositoryFixtures` with the expected language, package manager, test command, and one ecosystem-defining key file.

If the fixture requires special-case logic, first ask whether the rule is actually a common ecosystem convention. The portability suite should prevent maintainer-local assumptions from becoming global heuristics.
