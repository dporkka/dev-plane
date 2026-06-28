# Live End-to-End Integration Tests

The `apps/worker/internal/integration` package contains credential-gated live tests that exercise the full AI Dev Control Plane task lifecycle against real infrastructure:

- **NATS JetStream** event bus
- **Docker** workspace runtime
- Live **model providers** (OpenAI, Anthropic, Gemini, Groq, Fireworks)
- **GitHub** repository, branch push, and PR creation

These tests are skipped by default. Set `RUN_LIVE_E2E=1` to enable them.

## Quick Start

```bash
# 1. Start NATS (and Temporal if you use it)
make docker-up

# 2. Export credentials
export OPENAI_API_KEY=sk-...
export GITHUB_TOKEN=ghp_...
export GITHUB_TEST_OWNER=your-github-username-or-org
export GITHUB_TEST_REPO=disposable-test-repo

# 3. Run the gates
make live-e2e
```

## Required Environment Variables

| Variable | Purpose |
|---|---|
| `RUN_LIVE_E2E=1` | Enables the live tests (without this they skip). |
| `NATS_URL` | NATS URL. Defaults to `nats://localhost:4222`. |
| `GITHUB_TOKEN` | Token with `repo` scope for PR creation and private clone. |
| `GITHUB_TEST_OWNER` | Owner of the disposable GitHub repository. |
| `GITHUB_TEST_REPO` | Name of the disposable GitHub repository. |
| `WORKSPACE_RUNTIME` | Runtime for `TestLiveModelProviderRun`. Defaults to `docker`. |
| `WORKSPACE_BASE_DIR` | Optional base directory for local/Docker workspaces. |

At least one model provider key is required for `TestLiveModelProviderRun`:

- `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY`
- `GEMINI_API_KEY`
- `GROQ_API_KEY`
- `FIREWORKS_API_KEY`

## Test Repository Setup

Create a disposable public repository on GitHub. The repository must:

1. Exist and be writable by `GITHUB_TOKEN`.
2. Contain a `README.md` file (the model run reads it).
3. Be safe to pollute with test branches and pull requests.

Example:

```bash
mkdir dev-plane-live-e2e
cd dev-plane-live-e2e
git init
echo "# Dev Plane Live E2E" > README.md
git add README.md
git commit -m "initial"
git remote add origin https://github.com/YOUR_OWNER/dev-plane-live-e2e.git
git push -u origin main
```

## What Each Test Verifies

### `TestLiveModelProviderRun`

- Creates a task with a pre-built spec.
- Publishes `tasks.approved`.
- Worker provisions a Docker workspace and queues an implementer run.
- The implementer executes against a real model provider.
- Run completes and the worker generates a `review_reports` row.

### `TestLivePRCreation`

- Seeds a completed implementer run with a local workspace that already has a committed change.
- Publishes `review.completed` to trigger a `pr_create` approval request.
- Approves the request.
- Worker pushes the branch, creates a GitHub PR, and persists a `pull_requests` row.

### `TestLiveResumeAfterApproval`

- Seeds a queued implementer run with a local workspace.
- Uses a deterministic fake model that emits `request_approval` then `final_response`.
- Verifies the run pauses, an approval is created, and after approval the run resumes and step numbering continues.

## Timeout and Cost Notes

- `make live-e2e` sets a 20-minute timeout.
- `TestLiveModelProviderRun` will make one or more LLM calls to the configured provider. Using `gpt-4o-mini` or similar small models keeps cost under a few cents.
- The router selects the best available model automatically; set only the provider key for the cheapest model you want to use.

## Cleanup

The tests create:

- Docker containers and volumes named `dev-plane-*`
- Git branches named `agent/*` and `agent/live-e2e-*`
- GitHub pull requests in the disposable repository

Use `make docker-down-volumes` to remove Docker state, and delete closed PRs/branches from the test repo periodically.
