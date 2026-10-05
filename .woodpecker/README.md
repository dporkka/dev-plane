# Woodpecker + Dagger CI

Dev Plane uses Woodpecker as the forge-triggered scheduler and Dagger as the portable execution environment.

## Authority

The layers have deliberately narrow responsibilities:

1. **Forge** selects the immutable revision and emits the webhook/event.
2. **Woodpecker** clones that revision, verifies `git rev-parse HEAD == CI_COMMIT_SHA`, and supplies owned compute.
3. **Dagger** provisions the pinned toolchain and executes the repository-native checks.
4. **Make/npm** remain the behavioral source of truth for lint, test, and build semantics.

A successful run is evidence only for the exact `CI_COMMIT_SHA` that Woodpecker checked out.

## Canonical lane

`.woodpecker/dagger.yml` invokes `.dagger/ci/verify.dag` on the `bootstrap-ci` pool.

The Dagger contract preserves the current GitHub CI requirements:

- Node 24
- Go 1.26.8
- golangci-lint v2.13.2
- NATS 2.10 with JetStream during tests
- `make lint-go`
- web lint and typecheck
- TypeScript SDK typecheck
- Go + SDK tests
- web tests
- `make build`
- `git diff --check`

Dagger caches Go modules, the Go build cache, npm downloads, and its engine state. The workflow pins Dagger v0.20.3, matching the already-operated Adacavo Woodpecker/Dagger lane instead of introducing the moving Dagger 1.0-beta module API into this qualification change.

## Activation

The repository must be activated in the Woodpecker control plane so the forge webhook can create pipelines. Checking in this workflow cannot perform account/control-plane activation by itself.

Keep GitHub Actions as a non-authoritative compatibility lane while the personal-account hosted-runner admission issue persists. Do not interpret `runner_id=0` / zero-step GitHub jobs as source verification failures.

## Execution-plane migration

Keep the required lane on `pool: bootstrap-ci` until the shared `k8s-ci`/Kueue execution plane has repeated scheduling, execution, terminal-status, and cleanup evidence. Changing the Woodpecker execution pool must not change `.dagger/ci/verify.dag` or the repository verification semantics.

Nulang Cloud/Firecracker remains a future verification runtime behind the Workspace contract; it is not required for this bootstrap CI lane.
