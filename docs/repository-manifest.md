# Repository Manifest

`dev-plane.json` is the repository-owned contract for lifecycle commands that Dev Plane should execute.

## Why it exists

File-system inference is useful as a fallback, but it is ambiguous for polyglot repositories and monorepos. A checked-in manifest lets each repository state its canonical commands without coupling the repository to Dev Plane internals.

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
  }
}
```

Only declare commands the repository actually supports.

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
- `timeout_seconds`, when present, to be between 0 and 3600.

Runtime-specific execution paths may apply a lower hard timeout cap.

## Security boundary

The manifest is repository-owned configuration, not a source for secrets. Do not put credentials or secret values in it. Commands still pass through the existing command safety checks and runtime capability policy before execution.

## Compatibility

Repositories without `dev-plane.json` continue to use the existing auto-detection behavior. This allows gradual adoption across the portfolio without making Dev Plane a mandatory runtime dependency.
