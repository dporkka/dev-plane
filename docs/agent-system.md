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

| # | Tool | Description | Classification |
|---|------|-------------|----------------|
| 1 | `read_file` | Read file contents at a given path | Safe |
| 2 | `write_file` | Write content to a file (create or overwrite) | Destructive |
| 3 | `search_files` | Search for patterns across files using ripgrep | Safe |
| 4 | `apply_patch` | Apply a unified diff patch to files | Destructive |
| 5 | `run_command` | Run a shell command in the workspace | Dangerous |
| 6 | `list_directory` | List directory contents with metadata | Safe |
| 7 | `inspect_repo` | Get repository structure and language breakdown | Safe |
| 8 | `get_git_diff` | Get git diff of current changes | Safe |
| 9 | `create_commit` | Stage changes and create a git commit | Significant |
| 10 | `run_tests` | Run the test suite for the project | Safe |

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
| `spec` | Planner output passed to Implementer |
| `review` | Reviewer feedback to Implementer |
| `test_results` | Test Runner results to Reviewer |
| `security_report` | Security Reviewer findings to Release Manager |
| `approval_request` | Any agent requesting human approval |

### Mailbox Behavior

- Messages are persisted to the database per task
- Each pipeline stage reads the previous stage's output from the mailbox
- Messages are immutable once written
- Failed stages can re-read inputs on retry

## Agent Runner

The Runner in `packages/agents/agent.go` orchestrates agent execution with a state machine and retry policy.

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

| Parameter | Default | Description |
|-----------|---------|-------------|
| Max retries | 3 | Number of retry attempts |
| Initial delay | 5s | First retry wait time |
| Backoff multiplier | 2 | Exponential backoff factor |
| Max delay | 5m | Maximum wait between retries |

Retryable errors (network timeouts, transient failures) trigger automatic retry. Non-retryable errors (syntax errors, test failures) fail immediately.

### Timeout Policy

- Default timeout: 30 minutes per agent run
- Configurable per-task via `AGENT_TIMEOUT_MINUTES`
- Hard kill after timeout + 60 second grace period

### Token Tracking

Every agent execution tracks LLM token usage:

```go
type TokenUsage struct {
    PromptTokens     int   // Input tokens sent to LLM
    CompletionTokens int   // Output tokens from LLM
    TotalTokens      int   // Total tokens consumed
}
```

Token usage is persisted to `model_usage` table for cost tracking.

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

1. **Planner** receives the task, reads the repository, and produces a structured spec with acceptance criteria and file change plan. Output: `spec` message.

2. **Implementer** reads the spec, makes the required code changes, and requests approval for mutations or shell commands that policy marks as sensitive. Output: git commits + `changes` message.

3. **Reviewer** reads the changes and provides a code review. Output: persisted `review_reports` row and `review.completed` event, or a `review` mailbox message when another role needs to act on the feedback.

4. **Test Runner** executes tests to validate the changes. Output: `test_results` message.

5. **Security Reviewer** scans the changes for security issues. Output: `security_report` message.

6. **Release Manager** prepares the release (changelog, version bump, PR). PR creation uses an explicit `pr_create` approval and merge actions require approval. Output: GitHub pull request.

### Pipeline Shortcuts

| Trigger | Skipped Stages | Notes |
|---------|---------------|-------|
| Task has existing spec | Planner | Use provided spec directly |
| Review approves on first pass | Additional review rounds | Go straight to tests |
| All tests pass | Retries | Proceed to security |
| No security issues | Additional security rounds | Go to release |
| User cancels | All subsequent stages | Immediate halt |

### Human-in-the-Loop

Human approval is required at these gates:

1. **After Planner** - Approve/reject the implementation plan
2. **After Security Review** - Acknowledge any security findings
3. **Before PR merge** - Final approval to create the pull request

Approval requests appear in the UI with context from the agent that generated them. Approvers can comment and request changes before approving.
