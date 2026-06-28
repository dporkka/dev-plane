# Security

## Secret Handling

Secrets are referenced by name only throughout the system. Raw secret values are never logged, exposed in API responses, or stored in the event stream.

### Key Principles

- **Reference only** - All code references secrets by name (e.g., `secrets.GITHUB_TOKEN`), never by value
- **Encrypted at rest** - Secret values are encrypted using AES-256-GCM before database persistence
- **Versioned rotation** - Rotation deactivates previous encrypted values and creates one new active version
- **Keyring support** - `SECRET_ENCRYPTION_KEYS` accepts comma-separated `key-id:base64-32-byte-key` entries; the first key encrypts new values and older keys remain available for decrypting prior versions
- **Separate scopes** - Secrets are scoped to organization, optional project, and environment (dev/staging/prod)
- **Approval required** - Access to secret values is audited; capability-kernel policy may require approval depending on scope and role
- **Audit trail** - Every secret access (read, write, rotate) generates an audit event

### Secret Lifecycle

```
Registration -> Capability check -> Encryption -> Storage -> Access (audited, approved when policy requires) -> Rotation
```

## Agent Safety

The following constraints define the production security target. Agent-run tool calls and dangerous HTTP workspace actions are evaluated by the capability kernel, and Docker-backed runtime-provider operations route through the same authorization paths before execution.

| Rule | Enforcement |
|------|-------------|
| Never modify `main` directly | Policy engine denies pushes to `main` |
| Never merge automatically | All merges require human approval |
| Never deploy to production without approval | Deployment gates require explicit sign-off |
| Sandboxed execution by default | Docker provider enforces sandboxing when `WORKSPACE_RUNTIME=docker` is active; `ENABLE_SANDBOXED_RUNTIME` remains off by default in local dev |
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
| `run_tests` | Safe | Allow |
| `write_file` | Destructive | Ask |
| `apply_patch` | Destructive | Ask |
| `delete_file` | Destructive | Ask |
| `run_command` | Dangerous | Ask |
| `install_dependency` | Destructive | Ask |
| `call_mcp_tool` | Dangerous | Ask |
| `run_migration` | Dangerous | Ask |
| `create_commit` | Significant | Ask |
| `push_branch` | Significant | Ask |
| `open_pull_request` | Significant | Ask |
| `start_preview_server` | Significant | Ask |
| `stop_preview_server` | Significant | Ask |
| `network_request` | Sensitive | Ask |
| `access_secret` | Sensitive | Ask |
| `merge_pull_request` | Administrative | Admin only |
| `deploy` | Administrative | Admin only |
| `write_secret` | Administrative | Admin only |
| `rotate_secret` | Administrative | Admin only |
| `modify_policy` | Administrative | Admin only |
| `modify_budget` | Administrative | Admin only |
| `delete_workspace` | Administrative | Admin only |
| `destructive_db` | Administrative | Deny |

Conditional deny rules in the default engine also block pushes to `main`, production secret reads, and file deletions larger than 10 MB.

### Capability Evaluation Flow

```
Agent requests tool execution
    -> Kernel resolves resource type and action from operation
    -> Check RBAC permissions (if user is present)
    -> Evaluate policies, returning the most restrictive matching effect
    -> Check budget constraints (if budget is provided)
    -> Upgrade high-risk operations to ask when sandbox is isolated
    -> Determine approval requirement (ask or admin_only)
    -> Determine risk level
    -> Log action to audit stream
```

## Policy Engine

Policies define the authorization rules for operations. Each policy has:

- **Resource type** - The category being accessed (`file`, `command`, `secret`, `git`, `network`, `deploy`, etc.)
- **Action** - The operation (`read`, `write`, `execute`, `delete`, etc.)
- **Resource** - Specific file path, command, branch, or other target (used in conditions)
- **Effect** - `allow`, `ask`, `deny`, or `admin_only`
- **Conditions** - Optional matching constraints (e.g., `branch == "main"`, `scope == "production"`)

### Default Policy Rules

```
# Allow: non-destructive reads
allow_file_reads         file    read          -> allow
allow_repo_search        command search        -> allow
allow_static_analysis    command analyze       -> allow
allow_tests              command test          -> allow

# Ask: mutations and external actions
ask_file_writes          file    write         -> ask
ask_command_execute      command execute       -> ask
ask_dependency_install   command install       -> ask
ask_db_migrate           command migrate       -> ask
ask_git_commit           git     commit        -> ask
ask_git_push             git     push          -> ask
ask_network              network *             -> ask
ask_pr_create            git     create_pr     -> ask

# Deny: dangerous operations
deny_production_secrets  secret  read          -> deny   (scope == "production")
deny_destructive_db      command destructive_db -> deny
deny_large_delete        file    delete        -> deny   (min_size_mb >= 10)
deny_push_main           git     push          -> deny   (branch == "main")
deny_production_deploy   deploy  *             -> deny   (environment == "production")

# Admin-only: sensitive operations
admin_deploy_prod        deploy  *             -> admin_only  (environment == "production")
admin_merge_pr           git     merge         -> admin_only
admin_write_secrets      secret  write         -> admin_only
admin_rotate_secrets     secret  rotate        -> admin_only
admin_modify_policies    policy  *             -> admin_only
```

### Policy Evaluation Order

Policies are evaluated by finding all matching rules and returning the most restrictive effect (`admin_only` > `deny` > `ask` > `allow`). If no rule matches, the kernel's default (`ask`) is used. RBAC is checked first; `ask` and `admin_only` require approval or elevated privileges.

## Role-Based Access Control (RBAC)

RBAC provides organization-level permissions based on user roles: `owner`, `admin`, and `member`. The capability kernel checks RBAC before evaluating policies; if the authenticated user lacks permission for the requested resource type and action, the operation is denied immediately.

- **Owner** - Full access to all resources and actions.
- **Admin** - Can read and write files, execute commands, manage git, read networks and deployments, manage secrets, organizations, projects, users, policies, integrations, and audit logs.
- **Member** - Can read and write files, read and write git resources, read networks and projects, and perform task actions.

RBAC is the first gate; policy evaluation and sandbox checks apply after it.

## Audit Logs

All actions are persisted in the audit log with the following attributes:

| Field | Description |
|-------|-------------|
| `organization_id` | Scope of the action |
| `actor_type` | `user`, `agent`, `system` |
| `actor_id` | ID of the actor (optional) |
| `action` | The action performed |
| `resource_type` | Type of resource affected |
| `resource_id` | ID of the resource (optional) |
| `details` | JSON payload with additional context |
| `ip_address` | Source IP (for human actions, optional) |
| `user_agent` | User agent string (for human actions, optional) |
| `timestamp` | When the action occurred |

### Immutable Guarantee

Audit logs are append-only in the database (`audit_logs` table). The `audit` NATS stream exists for event publishing but is not currently the persistence layer and uses work-queue retention.

### Audit Events

```go
// audit.action.logged
type AuditEvent struct {
    OrganizationID string
    ActorType      string // user, agent, system
    ActorID        string  // optional
    Action         string
    ResourceType   string
    ResourceID     string  // optional
    Details        json.RawMessage
    IPAddress      string  // optional
    UserAgent      string  // optional
}
```

### Currently Audited Events

The following events are written by the audit logger today:

- Capability checks (`capability_check`)
- Budget violations (`budget_violation`)
- Model calls (`model_call`)
- Secret reads, writes, and rotations (`secret.read`, `secret.write`, `secret.rotate`)
