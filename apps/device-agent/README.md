# Dev Plane device MCP agent (local-only preview)

This **Linux-only, local stdio MCP adapter** is part of Dev Plane, the canonical development control plane. It is a minimal, explicitly scoped device-side entry point — not a second task scheduler, workflow engine, or authorization service. **It does not implement remote connectivity or internet-facing authentication.** Do not expose this process on a network or use it as a privileged remote executor.

This module intentionally uses an independent Go 1.23 `go.mod` so it can be built and tested on Fedora without compiling the main Dev Plane Go 1.26 workspace. Run with `GOWORK=off` from this directory. It is not yet attached to Dev Plane's API or runtime providers.

## Local quick start

```bash
cd apps/device-agent
GOWORK=off go test ./...
GOWORK=off go build -o ./devplane-device ./cmd/devplane
./devplane-device --workspace /absolute/path/to/safe/repository
```

The only MCP transport is newline-delimited JSON-RPC over stdin/stdout. The process fails if `--workspace` is missing. The MCP client must be locally authorized to start it.

### Tools

| Tool | Behavior |
| --- | --- |
| `workspaces_list` | Display the single, explicitly selected local workspace |
| `files_list` | List entries within that workspace, without following symlinks |
| `files_read` | Read up to 64 KiB of UTF-8 text using Linux `openat` + `O_NOFOLLOW` |
| `jobs_start` | Run a fixed Git inspection profile (`git-status`, `git-diff`, `git-log`) |
| `jobs_status` | Read bounded, ephemeral job results |
| `jobs_cancel` | Request cancellation of a running job |

No arbitrary shell, filesystem writes, remote execution, deployments or production credentials are exposed. Job results exist in memory only. Git inspection output can contain secrets from tracked files or commit messages; use an explicitly safe workspace.

## Integration contract

Dev Plane, not this agent, must own authenticated principals, device enrollment, durable receipts, approval decisions, workspace-to-repository identity, and job orchestration. Introduce an **outbound-only** connection only after the gateway verifies a principal × device × workspace × operation grant, and the device independently revalidates a signed short-lived grant. For mutations, delegate execution to `packages/runtimes.Provider` in a properly isolated workspace, using the existing `packages/execution` activation/epoch/receipt contracts. Do not expose `packages/runtimes.LocalProvider` to remote requests.

See [the integration ADR](../../docs/device-agent-integration.md) and [security review](docs/SECURITY.md).
