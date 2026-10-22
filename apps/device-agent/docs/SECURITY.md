# Device agent security boundary

**Status: experimental, local-only MCP over stdio. No remote listener, no privileged command interface, and no authorization to change files.**

1. Operator selects exactly one workspace with `--workspace`; the agent holds a directory FD and uses `openat`/`O_NOFOLLOW` for each file component, denying `..`, absolute paths, device files, and symlinks.
2. A conservative filename exclusion policy hides common credential files (`.env*`, `.gitconfig`, `.kube`, `.ssh`, `id_rsa*`, `credentials.*` etc.). **It does not classify all secrets.** Files with arbitrary names, tracked diffs and Git history can disclose credentials. Never point the agent at a sensitive checkout.
3. Local search uses the existing descriptor-relative no-symlink access, bounded resource limits, and short-lived integrity-checked cursors. Cursors are not access tokens, and no remote access is enabled.
4. Git job profiles have fixed arguments, a stripped environment, maximum 20-second duration, bounded output, two-concurrent-job limit, cancellation, and no shell. Git is not a sandbox. Avoid repositories with untrusted local Git configuration.
5. Only stdio MCP is enabled. Treat MCP client input as untrusted. The agent cannot serve user requests over a network or authenticate remote clients.
6. Before adding remote operations: Dev Plane API authenticates the human+MCP client and verifies org/repository ownership; the relay authenticates the device using a revocable key; **device-side** authorization verifies scoped, time-bounded operation grants; all execution results have durable receipts (including indeterminate outcomes after disconnect).
7. Remote mutations must run in an isolated provider. `LocalProvider` and Docker socket mounts are not a security boundary; use audited container/microVM isolation and never mount deployment credentials into untrusted workspaces.

### Verification

```bash
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
GOWORK=off go build ./cmd/devplane
```

This is a code-level proof for the local slice, **not** a penetration test or production qualification.
