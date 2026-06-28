# Architecture

## System Overview

AI Dev Control Plane is a multi-service Go + Next.js application that orchestrates AI agents to perform software development tasks. The system provides a secure, auditable platform where AI agents can plan, implement, review, and test code changes within isolated sandboxed environments, with human approval gates at critical decision points.

The platform follows a control plane pattern: the web UI and API manage the lifecycle of tasks, workspaces, and agent runs, while workers execute agent logic against workspace runtime environments. Agent-run tool calls and dangerous HTTP workspace actions flow through a policy engine and audit-backed capability kernel before execution.

## Service Architecture

```
+-------------------------------------------------------------+
|                        Client Layer                          |
|  +----------+  +----------+  +----------+  +----------+    |
|  | Web UI   |  | CLI      |  | GitHub   |  | API      |    |
|  | (Next.js)|  | (future) |  | Webhooks |  | Clients  |    |
|  +----+-----+  +----+-----+  +----+-----+  +----+-----+    |
+-------|-------------|-------------|-------------|------------+
        |             |             |             |
        +-------------+-------------+-------------+
                      |
+-------------------------------------------------------------+
|                        API Layer                             |
|              +---------------------------+                  |
|              |    API Server (Go)        |                  |
|              |  - Chi Router             |                  |
|              |  - Auth/AuthZ             |                  |
|              |  - REST API               |                  |
|              |  - Server-Sent Events     |                  |
|              |  - Agent Runner package   |                  |
|              +------------+--------------+                  |
+---------------------------|----------------------------------+
                            |
            +---------------+---------------+
            |                               |
+-----------v-----------+       +-----------v-----------+
|   Worker Pool (Go)    |       |   NATS JetStream      |
|   - Task Consumer     |       |   - Event Bus         |
|   - Run Executor      |       |   - Command Queue     |
|   - Approval Handlers |       |   - Event Store       |
+-----------+-----------+       +-----------+-----------+
            |                               |
            +---------------+---------------+
                            |
+-------------------------------------------------------------+
|           Runner Service (workspace runtime provider)        |
|                 local / docker / remote                      |
+----------------------------|--------------------------------+
                             |
+-------------------------------------------------------------+
|   Runtime Provider    |   Database Layer                    |
|   - Local Mode        |   - SQLite (local)                  |
|   - Docker            |   - Postgres (Neon, RDS, etc.)      |
|   - Remote runtime    |   - Goose Migrations                |
+-----------------------+-------------------------------------+
```

## Data Flow

```
1. Task Intake
   User creates a task via Web UI -> POST /api/tasks
   or GitHub webhook triggers auto-task creation

2. Spec Generation
   Spec generator builds a heuristic/template-based spec from task
   metadata and project config; a planner agent role is defined but
   not currently used for auto-generation

3. Workspace Creation
   System provisions workspace runtime -> clones repo ->
   creates git worktree -> prepares environment

4. Agent Run
   Pipeline executes: Implementer -> Reviewer -> Test Runner
   Each agent role chooses structured model actions for tools,
   final responses, approval requests, or mailbox handoffs
   Worker consumes handoffs once and queues addressed follow-on roles
   Human approval gates at security-critical steps

5. Review
   Worker runs the reviewer service when no follow-on handoff is waiting
   Review report is persisted to `review_reports`
   Code review output presented in UI
   Human reviewer approves/rejects with comments
   Review pass includes an automated security scan

6. PR
   On approval, Release Manager creates PR
   Changes merged to main via standard GitHub flow
   Workspace decommissioned, audit log persisted
```

## Technology Stack

### Frontend
- **Next.js 16** (App Router) - React framework with server components
- **TypeScript** - Type-safe development
- **Tailwind CSS 4** - Utility-first styling with dark theme
- **CodeMirror 6** - In-browser code editing
- **xterm.js** - Terminal UI (consuming SSE log streams)
- **React Flow** - Interactive graph visualizations
- **@tanstack/react-query** - Server state management
- **Zustand** - Client state management
- **Lucide React** - Icon system

### Backend
- **Go 1.25** - Primary backend language
- **Chi Router** - Lightweight HTTP router
- **SQLC** - Type-safe SQL code generation
- **Goose** - Database migrations
- **NATS Go Client** - Event streaming

### Database
- **SQLite** - Local development and single-node deployments
- **Postgres (Neon, RDS, etc.)** - Cloud deployments with serverless scaling
- **23 tables** - Organizations, Users, Repositories, Projects, Workspaces, Tasks, Agent Runs, Steps, Review Reports, Approvals, Policies, Audit Logs, Model Usage, Agent Messages, Integrations, Secret References, Secret Values, generated task specs, project configs, repository detection results, Pull Requests, Budgets, and Deployments

### Event System
- **NATS JetStream** - Durable event streaming
- **Subjects** - `tasks.*`, `agents.>`, `runs.*`, `review.*`, `approval.*`, `pr.*`, `webhooks.*`, `audit.>`

### Workflow Engine
- **NATS JetStream worker handlers** - All environments

### Runtime
- **Local runtime** - Implemented workspace runtime for trusted development
- **Docker runtime** - Implemented container provider with no runtime network, named workspace volumes, read-only rootfs, dropped capabilities, no-new-privileges, CPU/memory/PID limits, process reattachment, HTTP workspace operations, and agent-tool dispatch through the provider abstraction. Production use still requires live Docker isolation tests.

## Database Schema

### Core Tables (001-005)
| Table | Purpose |
|-------|---------|
| `organizations` | Multi-tenant org boundary |
| `users` | Authenticated users with roles |
| `projects` | Project grouping of tasks |
| `repositories` | Git repos with metadata |
| `workspaces` | Isolated dev environments |

### Task & Agent Tables (006-009)
| Table | Purpose |
|-------|---------|
| `tasks` | Task definitions with spec + status |
| `agent_runs` | Run records for agent pipeline |
| `agent_steps` | Individual step execution logs |
| `approvals` | Human approval requests/decisions |

### Governance Tables (010-013)
| Table | Purpose |
|-------|---------|
| `policies` | Capability policies (allow/ask/deny) |
| `audit_logs` | Immutable action audit trail |
| `model_usage` | LLM token/cost tracking |
| `integrations` | External service connections |

### Project & Spec Tables (015)
| Table | Purpose |
|-------|---------|
| `project_configs` | Detected repo commands and framework metadata |
| `task_specs` | Generated implementation specs and plans |
| `detection_results` | Historical repository detection output |

### Secret Storage Tables (016)
| Table | Purpose |
|-------|---------|
| `secret_references` | Metadata for encrypted secret handles |
| `secret_values` | Versioned encrypted secret ciphertext |

### Agent Coordination Tables (017-018)
| Table | Purpose |
|-------|---------|
| `agent_messages` | Durable mailbox handoffs between roles |

### Review Reports (019)
| Table | Purpose |
|-------|---------|
| `review_reports` | Persisted automated code review output |

### Pull Requests (020)
| Table | Purpose |
|-------|---------|
| `pull_requests` | GitHub pull request records |

### Budgets (021)
| Table | Purpose |
|-------|---------|
| `budgets` | Org/project/task budget and usage limits |

### Deployments (022)
| Table | Purpose |
|-------|---------|
| `deployments` | Deployment records and status |

### Relationships
```
organizations --< users
organizations --< repositories
organizations --< projects
projects --< tasks
repositories --< workspaces
repositories --< project_configs
repositories --< detection_results
workspaces --< tasks
tasks --< task_specs
tasks --< agent_runs
tasks --< agent_messages
tasks --< approvals
tasks --< pull_requests
tasks --< budgets
tasks --< deployments
agent_runs --< agent_steps
agent_runs --< review_reports
agent_runs --< pull_requests
organizations --< policies
organizations --< audit_logs
organizations --< model_usage
organizations --< integrations
organizations --< secret_references
organizations --< budgets
projects --< budgets
```

## Event Architecture

### NATS Streams

| Stream | Subjects | Retention |
|--------|----------|-----------|
| `TASKS` | `tasks.>` | Work queue |
| `AGENTS` | `agents.>` | Work queue |
| `RUNS` | `runs.>`, `review.>`, `approval.>`, `pr.>` | Work queue |
| `WEBHOOKS` | `webhooks.>` | Work queue |
| `AUDIT` | `audit.>` | Work queue |

### Event Flow

```
Task Created -> [NATS: tasks.created]
    -> Worker (task handler) creates heuristic spec
    -> Task approved -> [NATS: tasks.approved]
    -> Worker provisions workspace and triggers run -> [NATS: runs.triggered]
    -> Agent run executes via API Agent Runner -> [NATS: agents.run.started]
    -> Steps execute -> [NATS: agents.step.created / completed / failed]
    -> Run completes -> [NATS: agents.run.completed]
    -> Worker schedules next role or review -> [NATS: runs.triggered / review.completed]
    -> Approval-needed run pauses -> [NATS: approval.requested]
    -> Human approves/rejects -> [NATS: approval.approved / rejected]
    -> Worker creates PR or requeues run -> [NATS: runs.triggered]
    -> Task updated -> [NATS: tasks.completed / failed]
    -> Audit logged -> [NATS: audit.action.logged]
```

### Webhook Flow

```
GitHub Event -> [NATS: webhooks.received]
    -> Webhook processor validates signature
    -> Creates task if configured -> [NATS: tasks.created]
    -> Acknowledges processing -> [NATS: webhooks.processed]
```
