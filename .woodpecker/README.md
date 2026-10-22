# Woodpecker CI

This directory provides an owned CI execution lane for Dev Plane on the existing `bootstrap-ci` Woodpecker agent pool.

## Authority

`.github/workflows/ci.yml` and the repository's checked-in Make targets remain the behavioral source of truth. `ci.yml` mirrors those commands; it must not silently weaken or redefine them.

The Woodpecker lane exists so repository verification can continue when GitHub-hosted runner dispatch is unavailable. A green Woodpecker run is evidence only for the exact commit it executed.

## Activation

Woodpecker must be connected to `dporkka/dev-plane` as a repository before this workflow can run. Repository activation installs the forge webhook; adding this file alone cannot activate the repository.

After activation, trigger the workflow with a pull-request update or a manual Woodpecker run and verify that the reported revision is the intended PR HEAD.

## What `ci.yml` runs

The workflow uses the same versions and environment defaults as `.github/workflows/ci.yml`, starts a local NATS JetStream server, and executes the equivalent blocking checks in fail-fast order:

1. Go lint/vet.
2. Web lint and typecheck.
3. TypeScript SDK typecheck.
4. Go + SDK tests.
5. Web tests.
6. Full build.
7. `git diff --check`.

Do not use Woodpecker availability as a reason to remove GitHub CI. The two lanes should converge on the same repository-native commands so either runner fleet can provide exact-revision evidence.
