# Woodpecker + Dagger CI

Dev Plane uses Woodpecker as the forge-triggered scheduler and Dagger as the portable execution environment.

## Authority

The layers have deliberately narrow responsibilities:

1. **Forge** selects the immutable revision and emits the webhook/event.
2. **Woodpecker** clones that revision, verifies `git rev-parse HEAD == CI_COMMIT_SHA`, and supplies owned compute.
3. **Dagger** provisions the pinned toolchain and executes the repository-native checks.
4. **Make/npm** remain the behavioral source of truth for lint, test, and build semantics.

A successful run is evidence only for the exact `CI_COMMIT_SHA` that Woodpecker checked out.

## Security boundary

PR-controlled workflow code must never receive the Woodpecker host Docker socket. `.woodpecker/dagger.yml` therefore contains no host volume mounts and explicitly fails if `/var/run/docker.sock` is visible.

The Dagger Engine must be operator-managed outside repository-controlled workflow configuration and exposed through `_EXPERIMENTAL_DAGGER_RUNNER_HOST`, injected by the Woodpecker control plane (for example through `WOODPECKER_ENVIRONMENT`). Keep the engine on disposable/dedicated CI infrastructure with no tenant secrets or production credentials.

Dagger currently requires a privileged engine; the isolation boundary is therefore the dedicated CI execution host/VM, not the PR step container. Do not colocate this bootstrap engine with Nulang Cloud tenant or production Firecracker hosts.

## Canonical lane

`.woodpecker/dagger.yml` invokes `.dagger/ci/verify.dag` on the `bootstrap-ci` pool.

The Dagger contract preserves the current repository CI requirements:

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
- `git diff --check` over the committed PR range (or the latest main commit)

Dagger caches Go modules, the Go build cache, and npm downloads. The workflow pins Dagger v0.20.3, matching the already-operated Adacavo lane while keeping the execution engine outside PR-controlled Docker authority.

## Activation

The repository must be activated in the Woodpecker control plane so the forge webhook can create pipelines. Checking in this workflow cannot perform account/control-plane activation by itself.

Nulang Cloud's `deploy/woodpecker/ensure-repository.sh` provides the operator-only activation/repair path.

Keep GitHub Actions as a non-authoritative compatibility lane while the personal-account hosted-runner admission issue persists. Do not interpret `runner_id=0` / zero-step GitHub jobs as source verification failures.

## Execution-plane migration

Keep the required lane on `pool: bootstrap-ci` until the shared `k8s-ci`/Kueue execution plane has repeated scheduling, execution, terminal-status, and cleanup evidence. Changing the Woodpecker execution pool must not change `.dagger/ci/verify.dag` or the repository verification semantics.

Nulang Cloud/Firecracker remains the future stronger verification runtime behind the Workspace contract; it is not required for this bootstrap CI lane.
