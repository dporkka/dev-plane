# Repository Manifest

`dev-plane.json` is the repository-owned contract for lifecycle commands and changed-file validation that Dev Plane should execute.

## Why it exists

File-system inference is useful as a fallback, but it is ambiguous for polyglot repositories and monorepos. A checked-in manifest lets each repository state its canonical commands and its component dependency graph without coupling the repository to Dev Plane internals.

## Schema v1

```json
{
  "schema_version": 1,
  "name": "example",
  "commands": {
    "test": {
      "run": "make test",
      "timeout_seconds": 600
    },
    "lint": {
      "run": "make lint",
      "timeout_seconds": 120
    },
    "typecheck": {
      "run": "make typecheck"
    },
    "build": {
      "run": "make build"
    },
    "dev": {
      "run": "make dev"
    }
  },
  "validation": {
    "fallback_checks": ["lint", "test"]
  },
  "components": {
    "core": {
      "paths": ["packages/core/**"],
      "checks": {
        "lint": {
          "run": "cd packages/core && go vet ./..."
        },
        "test": {
          "run": "cd packages/core && go test ./..."
        }
      }
    },
    "api": {
      "paths": ["apps/api/**"],
      "depends_on": ["core"],
      "checks": {
        "test": {
          "run": "cd apps/api && go test ./..."
        }
      }
    }
  }
}
```

Only declare commands and dependency edges the repository actually supports.

## Changed validation

`dev-plane check --changed` reads the repository manifest, asks Git for changed tracked and untracked files, maps those files to components, expands reverse dependents, and runs the minimum declared checks.

Examples:

```sh
# Local working tree and untracked files.
dev-plane check --changed

# Include commits since a base revision, plus current working tree changes.
dev-plane check --changed --base origin/main

# Inspect the plan without executing commands.
dev-plane check --changed --dry-run

# Emit the plan as JSON for CI or agents.
dev-plane check --changed --dry-run --json
```

A component's `depends_on` list points from a consumer to the components it depends on. Therefore, a change to a dependency also validates all transitive consumers.

Component path patterns are repository-relative. Schema v1 supports exact or ordinary `path.Match` patterns plus a trailing `/**` form with a literal directory prefix for recursive directories. Malformed glob syntax is rejected.

If a changed file maps to no component, Dev Plane uses `validation.fallback_checks`. This is intentionally conservative: an incomplete component graph must cost extra validation, not silently skip it. When `fallback_checks` is omitted, every declared top-level validation command is used. Any manifest that declares components must therefore expose at least one top-level `lint`, `typecheck`, `test`, or `build` command so a conservative fallback always exists.

Components with no scoped checks also fall back to repository-wide checks.

## Check order

Changed validation uses a deterministic semantic order:

1. `lint`
2. `typecheck`
3. `test`
4. `build`

Duplicate commands with the same semantic kind are executed once.

## Dependency graph validation

The manifest fails closed when:

- a component references a missing dependency;
- the component graph contains a dependency cycle;
- a path escapes the repository;
- `**` is used anywhere except a trailing `/**`;
- a fallback check references an undeclared top-level command.

These checks make the component graph safe to use for CI admission and agent execution.

## Precedence

For agent test execution:

1. an explicit command supplied by the trusted caller;
2. the repository's `dev-plane.json` command;
3. stack-specific auto-detection.

If the manifest exists but is invalid, execution fails instead of silently falling back to an inferred command.

## Validation

Schema v1 requires:

- `schema_version` to equal `1`;
- every declared command to have a non-blank `run` value;
- `timeout_seconds`, when present, to be between 0 and 3600;
- every `depends_on` target to exist;
- the component dependency graph to be acyclic.

Runtime-specific execution paths may apply a lower hard timeout cap.

## Security boundary

The manifest is repository-owned configuration, not a source for secrets. Do not put credentials or secret values in it. Lifecycle commands invoked by agents still pass through the existing command safety checks and runtime capability policy before execution.

The local `dev-plane check --changed` command executes repository-owned checks through the shell in the selected repository, equivalent to running that repository's checked-in test commands directly. Use it only with repositories whose code you trust.

## Compatibility

Repositories without `dev-plane.json` continue to use the existing auto-detection behavior for agent test execution. This allows gradual adoption across the portfolio without making Dev Plane a mandatory runtime dependency.

Repositories can adopt changed validation incrementally. Unmapped files and components without scoped checks stay conservative through repository-wide fallback checks.
