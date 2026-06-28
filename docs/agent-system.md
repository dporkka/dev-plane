# Agent System

## Agent Roles

The system supports 7 specialized agent roles, each with distinct capabilities, tools, and approval requirements:

| # | Role | Name | Description | Approval |
|---|------|------|-------------|----------|
| 1 | `planner` | Planner | Analyzes tasks and creates detailed implementation plans with acceptance criteria | Required |
| 2 | `implementer` | Implementer | Writes code changes, tests, and documentation to fulfill the task specification | Auto |
| 3 | `reviewer` | Code Reviewer | Reviews code changes for quality, correctness, security, and adherence to best practices | Auto |
| 4 | `test_runner` | Test Runner | Executes tests, validates behavior against acceptance criteria, and reports results | Auto |
| 5 | `security_reviewer` | Security Reviewer | Analyzes code for security vulnerabilities, injection risks, and unsafe patterns | Required |
| 6 | `docs_writer` | Documentation Writer | Writes and updates documentation, README files, API docs, and inline comments | Auto |
| 7 | `release_manager` | Release Manager | Manages versioning, changelogs, release notes, and deployment preparation | Required |

### Role Configuration

Each role has a `RoleConfig` that defines:

- **System prompt** - The LLM prompt template for the role
- **Default model** - The LLM model used (default: `claude-sonnet-4-20250514`)
- **Allowed tools** - Which tools the role can access
- **Requires approval** - Whether human approval is needed before execution

## Agent Tools

All agents have access to the 10 standard tools:

| # | Tool | Description |
|---|------|-------------|
| 1 | `read_file` | Read file contents at a given path |
| 2 | `write_file` | Write content to a file (create or overwrite) |
| 3 | `search_files` | Search for patterns across files using ripgrep |
| 4 | `apply_patch` | Apply a unified diff patch to files |
| 5 | `run_command` | Run a shell command in the workspace |
| 6 | `list_directory` | List directory contents with metadata |
| 7 | `inspect_repo` | Get repository structure and language breakdown |
| 8 | `get_git_diff` | Get git diff of current changes |
| 9 | `create_commit` | Stage changes and create a git commit |
| 10 | `run_tests` | Run the test suite for the project |

### Tool Registry

Tools are managed through the `ToolRegistry` in `packages/agents/tools.go`:

```go
registry := agents.NewToolRegistry()     // Pre-populated with StandardTools
registry.Register(customTool)            // Add custom tools
registry.Get("read_file")                // Look up by name
registry.List()                          // Get all registered tools
```

## Agent Mailbox

The Agent Mailbox provides durable message passing between agents in a multi-agent pipeline. It persists messages so that pipeline stages can communicate even if one stage fails and is retried.

### Mailbox Types

| Message Type | Purpose |
|-------------|---------|
| `handoff` | Transfer control from one agent role to another |
| `review_comment` | Code review feedback |
| `blocker` | Blocking issue that pauses the pipeline |
| `escalation` | Escalation to a human or the system |
| `watchdog` | Timeout or health-check notification |
| `decision` | Recorded decision |
| `question` | Question from one agent to another or a human |
| `answer` | Answer to a `question` |

### Mailbox Behavior

- Messages are persisted to the database per task
- Each pipeline stage reads the previous stage's output from the mailbox
- Messages are immutable once written
- Failed stages can re-read inputs on retry

## Agent Runner

The production Runner is in `apps/api/internal/agentrunner/runner.go`; it executes tool-calling loops, persists steps, checks budget/capabilities, and streams events. The simpler `packages/agents/agent.go` defines role/tool models but is not used by the API/worker services.

### Status Machine

```
Pending -> Queued -> Running -> Completed
                      |
                      +---> Paused -> Queued
                      |
                      +---> Cancelled
                      |
                      +---> Failed
```

| Status | Description |
|--------|-------------|
| `pending` | Run created but not yet queued |
| `queued` | Waiting for a worker slot |
| `running` | Agent is actively executing |
| `paused` | Waiting on human approval before continuing |
| `completed` | All steps completed successfully |
| `failed` | One or more steps failed |
| `cancelled` | Run was cancelled by user |

### Retry Policy

> **Not implemented yet.** The worker does not apply an application-level retry policy; only NATS-level redelivery of unacknowledged messages is used.

### Timeout Policy

- Default timeout: 30 minutes per agent run (hardcoded in `apps/worker/internal/handlers/run_handlers.go:381`).
- Currently not configurable via environment variable.
- No separate grace-period kill is implemented.

### Token Tracking

Every agent execution tracks LLM token usage:

```go
type TokenUsage struct {
    PromptTokens     int   // Input tokens sent to LLM
    CompletionTokens int   // Output tokens from LLM
    TotalTokens      int   // Total tokens consumed
}
```

The production `AgentRun` model stores `PromptTokens`, `CompletionTokens`, and `TotalCost`; the `model_usage` table also records `total_tokens` for cost tracking.

## Multi-Agent Pipeline

The target pipeline executes agent roles in sequence with state passing between stages. The current API runner uses a model-driven structured action loop: each model turn returns one JSON action (`tool_call`, `final_response`, `handoff`, or `request_approval`). Tool calls are capability-gated, persisted as steps, budget-checked, streamed as events, and dispatched through the workspace runtime provider. `request_approval` actions persist approval records, publish `approval.requested`, and pause the run. When the approval is granted, the worker requeues the paused run and publishes `runs.triggered`; the runner reloads previous steps into the model prompt and continues with the next step number. Handoff actions are written to the durable `agent_messages` mailbox, loaded into prompts for the addressed role or broadcast recipients, consumed exactly once by the worker, and used to queue the next role. Worker `runs.triggered` handling dispatches queued runs to the shared API agent executor. Direct HTTP providers are implemented for OpenAI, Anthropic, Gemini, Groq, and Fireworks.

Remaining production work is to run and capture live end-to-end evidence with a configured model provider, runtime, and approval workflow.

```
+----------+    spec     +----------+   changes   +----------+
|  Planner | ----------> |Implementer| ---------> | Reviewer |
|          |             |           |            |          |
| Analyzes |             | Writes    |            | Reviews  |
| task and |             | code to   |            | for      |
| creates  |             | meet spec |            | quality  |
| spec     |             |           |            |          |
+----------+             +-----+-----+            +-----+----+
                                |                        |
                                v                        v
                         +----------+           +---------+
                         |TestRunner|           | Security|
                         |          |           |Reviewer |
                         | Validates|           |         |
                         | against  |           |Scans for|
                         | criteria |           |vulns    |
                         +----+-----+           +----+----+
                              |                      |
                              +----------+-----------+
                                         |
                                         v
                                   +----------+
                                   | Release  |
                                   | Manager  |
                                   |          |
                                   | Creates  |
                                   | PR       |
                                   +----------+
```

### Pipeline Flow

1. A heuristic **Spec Generator** (`apps/api/internal/spec/generator.go`) creates a structured spec from the task title and description. The task then moves through `spec_review` and `approved` statuses before an `implementer` agent run is queued.

2. **Implementer** reads the spec, makes the required code changes, and requests approval for mutations or shell commands that policy marks as sensitive. Output: git commits + `changes` message.

3. **Reviewer** reads the changes and provides a code review. Output: persisted `review_reports` row and `review.completed` event, or a `review_comment` mailbox message when another role needs to act on the feedback.

4. **Test Runner** executes tests to validate the changes. Output: test results.

5. **Security Reviewer** scans the changes for security issues. Output: security findings.

6. **PR creation** is handled by the worker's approval handler on `approval.approved` events with `approval_type = 'pr_create'`, using `packages/prfactory` to open the GitHub pull request.

### Pipeline Shortcuts

> These shortcuts describe the target design and are **not yet implemented**. Review generation is currently a single pass, and spec generation is not conditionally skipped.

### Human-in-the-Loop

The runner can pause an agent run and request human approval. Current approval types include:

1. **Capability approval** (`capability:{operation}`) - Paused when the capability kernel returns `RequiredApproval` for an operation.
2. **Risky-action approval** (`risky_action`) - Paused when the model emits a `request_approval` action.
3. **PR-create approval** (`pr_create`) - Created after `review.completed`; approving it opens the GitHub pull request.

Approval requests appear in the UI with context from the agent that generated them. Approvers can comment and request changes before approving.

## Implementation Notes

- Model/provider selection is routed through `apps/api/internal/modelrouter/router.go`.
- The agent loop enforces a hard maximum of 50 steps (`maxSteps := 50` in `runner.go`).
- The first agent run created for a task is always an `implementer` run.
