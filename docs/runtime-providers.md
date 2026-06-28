# Runtime Providers

## Provider Interface

All workspace runtimes implement the `Provider` interface defined in `packages/runtimes/runtime.go`:

```go
type Provider interface {
    // CreateWorkspace provisions a new workspace session from the given request.
    CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error)

    // DestroyWorkspace tears down a workspace session and cleans up resources.
    DestroyWorkspace(ctx context.Context, sessionID string) error

    // ExecuteCommand runs a command within the workspace session.
    ExecuteCommand(ctx context.Context, sessionID string, cmd Command) (*CommandResult, error)

    // ReadFile reads a file from the workspace at the given path.
    ReadFile(ctx context.Context, sessionID, path string) ([]byte, error)

    // WriteFile writes data to a file in the workspace at the given path.
    WriteFile(ctx context.Context, sessionID, path string, data []byte) error

    // ApplyPatch applies a unified diff patch within the workspace.
    ApplyPatch(ctx context.Context, sessionID, patch string) error

    // Snapshot captures the current state of the workspace.
    Snapshot(ctx context.Context, sessionID string) (*Snapshot, error)

    // Restore restores a workspace from a snapshot.
    Restore(ctx context.Context, sessionID string, snap *Snapshot) error

    // GetStatus returns the current status of a workspace session.
    GetStatus(ctx context.Context, sessionID string) (*SessionStatus, error)

    // StreamLogs returns a channel of log lines from the workspace session.
    StreamLogs(ctx context.Context, sessionID string) (<-chan LogLine, error)
}
```

### Session Lifecycle

```
Pending -> Ready -> Running -> Stopped/Error -> Destroyed
    |        |         |           |
    v        v         v           v
  Provisioning Complete  Active   Cleanup
```

### Status Definitions

| Status | Description |
|--------|-------------|
| `pending` | Workspace is being provisioned |
| `ready` | Workspace is ready for commands |
| `running` | A command is currently executing |
| `stopped` | Workspace was stopped (graceful) |
| `error` | Workspace encountered an error |
| `destroyed` | Workspace has been cleaned up |

### Session Resource Limits

| Resource | Default | Configurable |
|----------|---------|--------------|
| CPU | 1 core | Yes (`DOCKER_WORKSPACE_CPUS`) |
| Memory | 512 MB | Yes (`DOCKER_WORKSPACE_MEMORY`) |
| Disk | — | Not enforced by the runtime |
| Timeout | 60 seconds per command | Per-command `Command.Timeout` |
| Network | Disabled | No (always `--network none`) |

## Docker Runtime

The Docker runtime is the required production direction for untrusted code. `packages/runtimes/docker.go` implements workspace creation, command execution, file read/write, patching, git snapshots, restore, status, logs, cleanup, and process reattachment through Docker CLI commands. Do not treat it as production-certified until the live Docker integration suite proves isolation on the target host configuration.

### Target Behavior

```
1. CreateWorkspace called with repository info
2. Docker base image is resolved (default `alpine/git:latest`; pulled implicitly by `docker run` if missing)
3. Clone repository into host staging
4. Check out the requested branch in the staging repo
5. Start no-network container with named workspace volume and resource limits
6. Copy staged repository contents into the container workspace volume
7. Return session ID for subsequent operations
```

### Required Container Security

- **No privileged mode** - Containers run unprivileged
- **Read-only rootfs** - Root filesystem is read-only; writes go to the named workspace volume and tmpfs
- **No network** - `--network none` (not configurable)
- **Resource limits** - CPU, memory, and PID limits enforced via Docker/cgroups
- **Seccomp profile** - Default seccomp filter blocks dangerous syscalls
- **No host mounts** - Only workspace-specific named volumes are mounted

### Configuration

Set via environment variables (or the runner/worker CLI flags `--workspace-runtime` and `--workspace-base-dir`):

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKSPACE_RUNTIME` | `local` | Provider name: `local` or `docker` |
| `WORKSPACE_BASE_DIR` | `<os-temp>/ai-dev-control-plane-workspaces` | Base directory for workspace staging |
| `DOCKER_WORKSPACE_IMAGE` | `alpine/git:latest` | Image used for workspace containers |
| `DOCKER_WORKSPACE_MEMORY` | `512m` | Memory limit |
| `DOCKER_WORKSPACE_CPUS` | `1.0` | CPU limit |
| `DOCKER_WORKSPACE_PIDS` | `256` | PID limit |

### Current Implementation

Located in `packages/runtimes/docker.go`. The provider is dependency-light and shells out to the Docker CLI. Current behavior:

| Method | Description |
|--------|-------------|
| `CreateWorkspace` | Clones repo into host staging, checks out the workspace branch, creates a named Docker volume, starts a hardened no-network container, and copies the staged repo into `/workspace` |
| `AttachSession` | Reattaches API or runner processes to an existing Docker session using deterministic container and volume names from `runtime_session_id` |
| `DestroyWorkspace` | Stops/removes container, removes the named workspace volume, and removes local staging data |
| `ExecuteCommand` | Runs command via `docker exec` with timeout |
| `ReadFile` | Reads through `docker exec` after rejecting absolute/traversal paths |
| `WriteFile` | Writes through `docker exec -i` after rejecting absolute/traversal paths |
| `ApplyPatch` | Applies patch via `docker exec` |
| `Snapshot` | Creates a git commit inside the workspace container |
| `Restore` | Resets the workspace git repository to a snapshot commit |
| `GetStatus` | Queries container state via Docker inspect |
| `StreamLogs` | Streams `docker logs -f` into runtime log events |

Remaining work:

- Run `RUN_DOCKER_INTEGRATION=1 go test ./...` in `packages/runtimes` on a Docker-enabled host before production certification; the gated suite creates a real session and asserts host-path isolation, no runtime network, cleanup, and resource limits.
- Replace Docker CLI shell-out with a typed Docker API client if operational needs require richer error handling or daemon connection management.

## Local Runtime

The Local runtime runs workspaces directly on the host machine. It is intended only for **trusted local development environments** where Docker is unavailable.

### Security Warning

> **WARNING**: The Local runtime provides no isolation. Agent code runs directly on the host with the same permissions as the API server process. **Never use in production or for untrusted code.**

### How It Works

```
1. CreateWorkspace creates a session directory under `WORKSPACE_BASE_DIR`
2. Clones repository into the session directory
3. Creates a git worktree within the same repo
4. All commands run directly on the host
5. Cleanup removes the session directory
```

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKSPACE_RUNTIME` | `local` | Must be `local` |
| `WORKSPACE_BASE_DIR` | `<os-temp>/ai-dev-control-plane-workspaces` | Directory for local worktrees |

### Implementation

Located in `packages/runtimes/local.go`. Key methods mirror the Docker implementation but execute directly on the host filesystem and shell.

## Remote Runner

The `runner` service (`apps/runner`) exposes any in-process provider over HTTP. The API and worker can route all runtime calls to a remote runner by setting:

| Variable | Description |
|----------|-------------|
| `RUNNER_URL` | Runner HTTP endpoint, e.g. `http://localhost:8082` |
| `RUNNER_AUTH_TOKEN` | Shared bearer token for runner requests |
| `RUNNER_PORT` | Runner listen port (default `8082`) |

`packages/runtimes/client.go` provides `RemoteProvider`, which implements the same `Provider` interface via the runner API.

## Future Providers

> These providers are not implemented yet. The only supported providers are `local` and `docker`.

### gVisor

Planned integration with [gVisor](https://gvisor.dev/) for stronger isolation than Docker alone. gVisor provides a userspace kernel that intercepts syscalls, adding an additional security boundary between the agent and the host.

Benefits:
- Stronger syscall filtering
- Additional kernel isolation layer
- Minimal performance overhead for typical workloads

Status: Planned (Phase 3)

### Firecracker

Planned integration with [AWS Firecracker](https://firecracker-microvm.github.io/) for microVM-based isolation. Each workspace would run in its own lightweight VM, providing hardware-level isolation.

Benefits:
- Hardware-level isolation per workspace
- Sub-100ms startup times
- Ideal for untrusted code execution

Status: Planned (Phase 4)

### Kubernetes

Planned support for running workspaces as Kubernetes pods. This enables elastic scaling and integration with existing K8s infrastructure.

Benefits:
- Elastic scaling via HPA
- Integration with existing K8s clusters
- Pod-level resource management
- Network policies for egress control

Status: Planned (Phase 3)
