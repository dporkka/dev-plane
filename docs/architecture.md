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
|                    +------------------+                      |
|                    | API Server (Go)  |                      |
|                    | - Chi Router     |                      |
|                    | - Auth/AuthZ     |                      |
|                    | - REST API       |                      |
|                    | - WebSocket      |                      |
|                    +--------+---------+                      |
+-----------------------------|--------------------------------+
                              |
            +-----------------+------------------+
            |                                    |
+-----------v-----------+            +-----------v-----------+
|   Worker Pool (Go)    |            |   NATS JetStream      |
|   - Task Consumer     |            |   - Event Bus         |
|   - Agent Orchestrator|            |   - Command Queue     |
|   - Step Runner       |            |   - Event Store       |
+-----------+-----------+            +-----------+-----------+
            |                                    |
+-----------v-----------+            +-----------v-----------+
|   Agent Runner (Go)   |            |   Temporal / Local    |
|   - Model action loop |            |   - Workflow Engine   |
|   - Tool executor     |            |   - Task Scheduling   |
|   - Token tracking    |            |   - Retry Policies    |
+-----------+-----------+            +-----------+-----------+
            |                                    |
+-----------v-----------+            +-----------v-----------+
|   Runtime Provider    |            |   Database Layer      |
|   - Local Mode        |            |   - SQLite (local)    |
|   - Docker            |            |   - Neon Postgres     |
|   - Future: gVisor    |            |   - Goose Migrations  |
+-----------------------+            +-----------------------+
```

## Data Flow

```
1. Task Intake
   User creates a task via Web UI -> POST /api/tasks
   or GitHub webhook triggers auto-task creation

2. Spec Generation
   Planner agent analyzes task -> reads repo -> generates spec
   with acceptance criteria and file change plan

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
   Security reviewer scans for vulnerabilities

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
- **xterm.js** - Terminal emulation for logs
- **React Flow** - Interactive graph visualizations
- **@tanstack/react-query** - Server state management
- **Zustand** - Client state management
- **Lucide React** - Icon system

### Backend
- **Go 1.23** - Primary backend language
- **Chi Router** - Lightweight HTTP router
- **SQLC** - Type-safe SQL code generation
- **Goose** - Database migrations
- **NATS Go Client** - Event streaming
- **Temporal SDK** - Workflow orchestration

### Database
- **SQLite** - Local development and single-node deployments
- **Neon Postgres** - Cloud deployments with serverless scaling
- **20 tables** - Organizations, Users, Repositories, Projects, Workspaces, Tasks, Agent Runs, Steps, Review Reports, Approvals, Policies, Audit Logs, Model Usage, Agent Messages, Integrations, Secret References, Secret Values, generated task specs, project configs, and repository detection results

### Event System
- **NATS JetStream** - Durable event streaming
- **Subjects** - `tasks.*`, `agents.>`, `runs.*`, `review.*`, `approval.*`, `pr.*`, `webhooks.*`, `audit.>`

### Workflow Engine
- **Temporal (cloud)** - Production workflow orchestration
- **Local runner** - Development mode task execution

### Runtime
- **Local runtime** - Implemented workspace runtime for trusted development
- **Docker runtime** - Implemented container provider with no runtime network, named workspace volumes, read-only rootfs, dropped capabilities, no-new-privileges, CPU/memory/PID limits, process reattachment, HTTP workspace operations, and agent-tool dispatch through the provider abstraction. Production use still requires live Docker isolation tests.

## Database Schema

### Core Tables (001-005)
| Table | Purpose |
|-------|---------|
| `organizations` | Multi-tenant org boundary |
| `users` | Authenticated users with roles |
| `repositories` | Git repos with metadata |
| `projects` | Project grouping of tasks |
| `workspaces` | Isolated dev environments |

### Task & Agent Tables (006-009)
| Table | Purpose |
|-------|---------|
| `tasks` | Task definitions with spec + status |
| `agent_runs` | Run records for agent pipeline |
| `agent_steps` | Individual step execution logs |
| `review_reports` | Persisted automated code review output |
| `approvals` | Human approval requests/decisions |

### Governance Tables (010-013)
| Table | Purpose |
|-------|---------|
| `policies` | Capability policies (allow/ask/deny) |
| `audit_logs` | Immutable action audit trail |
| `model_usage` | LLM token/cost tracking |
| `integrations` | External service connections |

### Agent Coordination & Secret Tables (014-017)
| Table | Purpose |
|-------|---------|
| `task_specs` | Generated implementation specs and plans |
| `project_configs` | Detected repo commands and framework metadata |
| `detection_results` | Historical repository detection output |
| `secret_references` | Metadata for encrypted secret handles |
| `secret_values` | Versioned encrypted secret ciphertext |
| `agent_messages` | Durable mailbox handoffs between roles |

### Relationships
```
organizations --< users
organizations --< repositories
organizations --< projects
projects --< tasks
repositories --< workspaces
workspaces --< tasks
tasks --< agent_runs
agent_runs --< agent_steps
agent_runs --< review_reports
tasks --< agent_messages
tasks --< approvals
organizations --< policies
organizations --< audit_logs
organizations --< model_usage
organizations --< integrations
organizations --< secret_references
```

## Event Architecture

### NATS Streams

| Stream | Subjects | Retention |
|--------|----------|-----------|
| `TASKS` | `tasks.>` | Work queue |
| `AGENTS` | `agents.>` | Limits (10K) |
| `RUNS` | `runs.>`, `review.>`, `approval.>`, `pr.>` | Work queue |
| `WEBHOOKS` | `webhooks.>` | Limits (5K) |
| `AUDIT` | `audit.>` | Forever |

### Event Flow

```
Task Created -> [NATS: tasks.created]
    -> Worker picks up -> [NATS: tasks.started]
    -> Planner runs -> [NATS: agents.run.started]
    -> Spec approved -> [NATS: tasks.approved]
    -> Worker provisions workspace and queues implementer -> [NATS: runs.triggered]
    -> Steps execute -> [NATS: agents.step.*]
    -> Run completes -> [NATS: agents.run.completed]
    -> Handoff consumed -> [NATS: runs.triggered]
    -> Worker executes queued run via shared agent executor
    -> Approval-needed run pauses -> [NATS: approval.requested]
    -> Human approves -> [NATS: approval.approved]
    -> Worker requeues paused run -> [NATS: runs.triggered]
    -> If no handoff remains, reviewer persists report -> [NATS: review.completed]
    -> Worker requests human approval -> [NATS: approval.requested]
    -> Task updated -> [NATS: tasks.completed]
    -> Audit logged -> [NATS: audit.action.logged]
```

### Webhook Flow

```
GitHub Event -> [NATS: webhooks.received]
    -> Webhook processor validates signature
    -> Creates task if configured -> [NATS: tasks.created]
    -> Acknowledges processing -> [NATS: webhooks.processed]
```
