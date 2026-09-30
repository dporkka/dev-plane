# Repository Protocol v1

Dev Plane treats each repository as the source of truth for how that repository is developed and verified. The repository contract declares commands, verification profiles, work isolation, review expectations, and path-based risk gates without teaching Dev Plane the repository's build system.

## Contract file

Repositories opt in with a root `devplane.yaml`.

```yaml
version: 1

commands:
  doctor: make doctor
  dev: make dev
  logs: make logs

verification:
  fast:
    command: make verify-fast
  changed:
    command: make verify-changed
  full:
    command: make ci

work:
  isolation: worktree
  max_parallel_cost: 4
  require_clean_handoff: true

review:
  independent: true
  exact_head: true

risk:
  - paths:
      - auth/**
      - db/migrations/**
      - infra/**
    level: high
    requires:
      - full
      - independent_review
      - human_approval
```

The parser is strict: unknown fields are rejected so misspelled safety policy cannot silently degrade behavior.

## Verification profiles

Profile names are repository-defined. Dev Plane treats them as opaque named gates and executes the declared command. This keeps repository-specific tooling inside the repository: Make, Cargo, npm, Dagger, Bazel, shell scripts, or other build systems can all satisfy the same control-plane contract.

Two gates are reserved by the protocol:

- `independent_review`
- `human_approval`

Risk rules can require either reserved gate or any declared verification profile.

## Work items

A durable work item records:

- repository
- objective and acceptance criteria
- parent and discovery lineage
- dependencies
- ownership paths
- risk
- estimated cost
- required gates
- immutable base SHA
- claim/state metadata

Ownership paths are repository-relative. Escaping paths such as `../secrets/**` are rejected.

## Evidence

Verification evidence is bound to an exact candidate HEAD. A new commit invalidates the previous evidence by definition because `EvidenceBundle.ValidateHead` requires the bundle's `head_sha` to equal the candidate SHA and requires every recorded gate to be passing.

This is the base invariant for later review, CI, handoff, and landing integration:

```text
evidence.head_sha == candidate.head_sha
```

## Scope of v1

This package defines and validates the contract. It intentionally does not yet:

- schedule `WorkItem` values
- persist work items or evidence
- execute verification commands
- create worktrees
- perform independent review
- land pull requests

Those behaviors should adapt the existing scheduler/runtime/reviewer primitives to this contract instead of duplicating them.
