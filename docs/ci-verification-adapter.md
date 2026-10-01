# CI verification adapter

Dev Plane exposes repository verification through the same runtime and evidence semantics used by autonomous coding agents. The forge CI system should supply an immutable checkout and status reporting; Dev Plane owns workspace lifecycle, exact-head verification, repository-defined gates, and structured evidence.

## Repository contract

A repository opts in with root `devplane.yaml`. Verification commands remain repository-owned. CI configuration should select named gates rather than duplicate their command bodies.

## CLI

Given an already checked-out exact commit:

```bash
dev-plane verify \
  --provider=local \
  --source=. \
  --repository=owner/repo \
  --candidate-sha="$CI_COMMIT_SHA" \
  --base-sha="$CI_COMMIT_TARGET_SHA" \
  --gate=quality \
  --changed-path=apps/web/src/example.ts
```

The command:

1. resolves the host checkout HEAD and fails before provisioning if it differs from `--candidate-sha`;
2. creates an ephemeral workspace through `runtimes.Provider`;
3. reads `devplane.yaml` inside that workspace;
4. runs explicitly requested and path-risk-required gates;
5. verifies the workspace HEAD before and after every executable gate;
6. atomically writes `.devplane/verification-evidence.json` by default;
7. destroys the workspace and fails the command if teardown fails;
8. writes a compact JSON result to stdout for forge/status adapters.

`--gate` and `--changed-path` may each be repeated.

## Local bootstrap mode

`--provider=local` is usable now on trusted CI workers. It re-clones the already authenticated local checkout into a disposable runtime directory, so verification policy can migrate to Dev Plane before Firecracker becomes the default execution backend.

The local provider is a bootstrap path, not the target isolation boundary for untrusted code.

## Nulang Cloud mode

`--provider=nulang` uses:

- `DEV_PLANE_NULANG_URL` (or `--runtime-url`) for the Workspace API;
- `DEV_PLANE_NULANG_INTERNAL_TOKEN` for the internal service credential;
- the current checked-out repository as the trusted source snapshot.

The Nulang source seeder validates the local checkout HEAD before the first guest mutation, clones without hardlinks into trusted staging, removes or sanitizes the Git remote, transfers a tar through the chunked Workspace file API, extracts inside the guest, and verifies the guest HEAD again.

Git credentials are not required inside the guest and are not persisted in the transferred `.git/config`.

### Current production gate

The current Nulang Workspace unary exec contract intentionally caps one command at 50 seconds while streaming/asynchronous execution is incomplete. Repository profiles that require longer timeouts therefore remain on the bootstrap execution path. Do not weaken verification timeouts merely to fit this transport limitation; qualify long-running execution first.

## Adapter image

`infra/docker/cli.Dockerfile` builds a small adapter image containing only the static `dev-plane` binary, Git, and CA certificates. The image is intended for the eventual forge-facing/Nulang-backed adapter. Repository toolchains should live inside the selected verification workspace rather than accumulating in the adapter image.

## Woodpecker target shape

```text
Git forge
   |
   v
Woodpecker webhook/status plane
   |
   v
dev-plane verify --provider=nulang
   |
   v
Nulang Cloud Workspace / Firecracker
   |
   v
exact-head EvidenceBundle
   |
   v
Woodpecker / forge status
```

Until the Nulang long-running execution gate is qualified, keep the existing repository-specific Woodpecker bootstrap lanes while moving their command policy into `devplane.yaml` and repository-owned scripts.
