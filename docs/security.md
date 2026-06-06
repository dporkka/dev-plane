# Security

## Secret Handling

Secrets are referenced by name only throughout the system. Raw secret values are never logged, exposed in API responses, or stored in the event stream.

### Key Principles

- **Reference only** - All code references secrets by name (e.g., `secrets.GITHUB_TOKEN`), never by value
- **Encrypted at rest** - Secret values are encrypted using AES-256-GCM before database persistence
- **Versioned rotation** - Rotation deactivates previous encrypted values and creates one new active version
- **Keyring support** - `SECRET_ENCRYPTION_KEYS` accepts comma-separated `key-id:base64-32-byte-key` entries; the first key encrypts new values and older keys remain available for decrypting prior versions
- **Separate scopes** - Secrets are scoped to organization + environment (dev/staging/prod)
- **Approval required** - Access to secret values requires explicit administrative approval
- **Audit trail** - Every secret access (read, write, rotate) generates an audit event

### Secret Lifecycle

```
Registration -> Capability check -> Encryption -> Storage -> Access (with approval) -> Rotation
```

## Agent Safety

The following constraints define the production security target. Agent-run tool calls and dangerous HTTP workspace actions are evaluated by the capability kernel, and Docker-backed runtime-provider operations route through the same authorization paths before execution.

| Rule | Enforcement |
|------|-------------|
| Never modify `main` directly | Pre-commit hook rejects pushes to main |
| Never merge automatically | All merges require human approval |
| Never deploy to production without approval | Deployment gates require explicit sign-off |
| Sandboxed execution by default | Docker provider creates no-network containers with read-only rootfs, named workspace volumes, dropped capabilities, no-new-privileges, and CPU/memory/PID limits; live Docker integration tests are still required before production use |
| No network egress by default | Workspaces have no outbound network access unless explicitly allowed |
| Read-only by default | File system writes require capability grants |
| Time bounded | All agent runs have a configurable timeout (default 30 min) |

## Capability Kernel

Agent-run tool calls go through the Capability Kernel, a centralized authorization layer that evaluates tool calls before execution. Direct workspace HTTP writes, patches, and shell commands are also gated by the kernel. In the composed API server, the kernel is backed by the audit logger so capability checks persist `capability_check` rows.

### Tool Classification

| Tool | Classification | Default Policy |
|------|---------------|----------------|
| `read_file` | Safe | Allow |
| `search_files` | Safe | Allow |
| `list_directory` | Safe | Allow |
| `inspect_repo` | Safe | Allow |
| `get_git_diff` | Safe | Allow |
| `write_file` | Destructive | Ask |
| `apply_patch` | Destructive | Ask |
| `run_command` | Dangerous | Ask |
| `run_tests` | Safe | Allow |
| `create_commit` | Significant | Ask |
| `run_tests` | Safe | Allow |

### Capability Evaluation Flow

```
Agent requests tool execution
    -> Kernel resolves policy for (tool, agent_role, resource)
    -> Check policy:
        ALLOW  -> Execute immediately
        ASK    -> Create approval request, pause execution
        DENY   -> Reject with error
    -> Log action to audit stream
```

## Policy Engine

Policies define the authorization rules for agent behavior. Each policy has:

- **Subject** - The agent role or user
- **Action** - The tool or operation
- **Resource** - File path, command pattern, or branch
- **Effect** - `allow`, `ask`, `deny`, or `admin-only`

### Default Policy Rules

```
# Planner can read everything, needs approval for spec changes
planner read_file * -> allow
planner write_file *.md -> ask
planner write_file * -> admin-only

# Implementer can write code, needs approval for destructive ops
implementer read_file * -> allow
implementer write_file * -> ask
implementer apply_patch * -> ask
implementer run_tests * -> allow
implementer run_command * -> ask
implementer create_commit * -> ask
implementer push_branch * -> ask

# Reviewer is read-only
reviewer read_file * -> allow
reviewer * * -> deny

# Security reviewer can read everything
security_reviewer read_file * -> allow
security_reviewer * * -> deny

# Release manager needs approval for all actions
release_manager * * -> admin-only
```

### Policy Evaluation Order

1. Explicit deny rules are checked first
2. Explicit allow rules are checked second
3. `ask` rules trigger approval workflow
4. `admin-only` requires admin role
5. Default ask if no rule matches

## Audit Logs

All actions are persisted in the audit log with the following attributes:

| Field | Description |
|-------|-------------|
| `organization_id` | Scope of the action |
| `actor_type` | `user`, `agent`, `system`, `webhook` |
| `actor_id` | ID of the actor |
| `action` | The action performed |
| `resource_type` | Type of resource affected |
| `resource_id` | ID of the resource |
| `details` | JSON payload with additional context |
| `ip_address` | Source IP (for human actions) |
| `timestamp` | When the action occurred |

### Immutable Guarantee

Audit logs are append-only. Once written, they cannot be modified or deleted. The audit stream uses NATS with infinite retention, providing a tamper-evident log of all system activity.

### Audit Events

```go
// audit.action.logged
type AuditEvent struct {
    OrganizationID string
    ActorType      string // user, agent, system, webhook
    ActorID        string
    Action         string
    ResourceType   string
    ResourceID     string
    Details        json.RawMessage
    IPAddress      string
}
```

### Critical Audit Events

- Task created/approved/started/completed/failed
- Agent run started/completed/failed
- Approval requested/approved/rejected
- Policy created/modified/deleted
- Secret written/accessed/rotated
- Workspace created/destroyed
- Repository connected/disconnected
- User login/logout/role change
