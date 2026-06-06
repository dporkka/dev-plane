# AI Dev Control Plane

> A production-grade, self-hostable AI development control plane. Takes tasks from
> prompts, GitHub issues, Linear tickets, Slack/Discord commands, or voice and turns
> them into isolated branches, code changes, tests, reviews, pull requests, and
> deployment-gated releases.

[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go)](https://go.dev)
[![Next.js](https://img.shields.io/badge/Next.js-16-000000?logo=next.js)](https://nextjs.org)
[![NATS](https://img.shields.io/badge/NATS-JetStream-27AAE1?logo=nats.io)](https://nats.io)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## Architecture

```
 +-------------------+       +-------------------+       +-------------------+
 |   Next.js 16      |<----->|   Go API Server   |<----->|   NATS JetStream  |
 |   (Port 3000)     |  HTTP |   (Port 8080)     |  NATS |   (Port 4222)     |
 |                   |       |                   |       |                   |
 | - CodeMirror 6    |       | - Chi Router      |       | - Event Bus       |
 | - shadcn/ui       |       | - SQLC + Goose    |       | - Task Streams    |
 | - Zustand         |       | - JWT + OAuth     |       | - Pub/Sub         |
 | - TanStack Query  |       | - SSE Stream      |       | - Persistence     |
 +-------------------+       +--------+----------+       +-------------------+
                                      |
                                      |  gRPC / HTTP
                                      v
 +-------------------+       +-------------------+       +-------------------+
 |   Go Worker       |<----->|   Go Runner       |       |   Temporal        |
 |   (Background)    |  NATS |   (Port 8082)     |       |   (Port 7233)     |
 |                   |       |                   |       |                   |
 | - Task Processor  |       | - Docker Sandbox  |       | - Workflows       |
 | - Agent Orchestr. |       | - Git Operations  |       | - Durability      |
 | - Event Consumer  |       | - Code Execution  |       | - (Optional)      |
 +-------------------+       +-------------------+       +-------------------+

 +-------------------+       +-------------------+
 |   GitHub App      |       |   AI Providers    |
 |   (Webhooks)      |       |   (Bifrost/Direct)|
 |                   |       |                   |
 | - Issue Sync      |       | - OpenAI          |
 | - PR Management   |       | - Anthropic       |
 | - Webhook Events  |       | - Bifrost Gateway |
 +-------------------+       +-------------------+
```

---

## Tech Stack

### Frontend
- **Next.js 16** with App Router
- **React 19** + TypeScript 5
- **Tailwind CSS 4** + shadcn/ui components
- **CodeMirror 6** for code editing
- **Zustand** for state management
- **TanStack Query** for API fetching
- **Server-Sent Events** for live updates

### Backend
- **Go 1.23+** with Chi router
- **SQLC** for type-safe database queries
- **Goose** for database migrations
- **log/slog** for structured logging
- **JWT** + OAuth2 for authentication

### Infrastructure
- **NATS JetStream** for event streaming
- **Temporal** (optional) for durable workflows
- **SQLite** (local) / **PostgreSQL** (cloud)
- **Docker** + Docker Compose for local dev

---

## Quick Start

### Prerequisites

- [Go 1.23+](https://go.dev/dl/)
- [Node.js 20+](https://nodejs.org/)
- [Docker](https://docs.docker.com/get-docker/) & Docker Compose
- [Git](https://git-scm.com/)

### 1. Clone & Setup

```bash
# Clone the repository
git clone <repo-url> ai-dev-control-plane
cd ai-dev-control-plane

# Copy environment template
cp .env.example .env

# (Optional) Edit .env with your GitHub App credentials
# nano .env
```

### 2. Start Development Environment

```bash
# Start everything (Docker services + all apps)
make dev

# Or start services individually:
make docker-up       # Start NATS, Temporal (optional)
make migrate         # Run database migrations
make dev-api         # Start API server (port 8080)
make dev-web         # Start Next.js (port 3000)
make dev-worker      # Start worker service
```

### 3. Access the Application

| Service | URL | Description |
|---|---|---|
| Web UI | http://localhost:3000 | Next.js frontend |
| API | http://localhost:8080 | Go API server |
| NATS | nats://localhost:4222 | Message bus |
| NATS Monitor | http://localhost:8222 | NATS dashboard |
| Temporal UI | http://localhost:8233 | Workflow UI (if enabled) |

### Optional AgentVault Logging

Dev Plane can capture task lifecycle events into a local AgentVault inbox:

```bash
AGENTVAULT_URL=http://127.0.0.1:47321
AGENTVAULT_TOKEN=<token printed by agentvault serve>
AGENTVAULT_PROJECT=dev-plane
```

When configured, task creation events are posted to AgentVault's `/capture` endpoint. Logging is best-effort; Dev Plane continues working if AgentVault is offline.

### Dev Plan Brief Handoff

Dev Plan Builder's Brief bundles can create implementation tasks through:

```http
POST /api/v1/projects/{projectID}/brief-handoffs
```

The endpoint accepts `repository_id` plus a `brief_url`, `brief_zip_url`, or inline `documents` array, then creates a normal task with `source=dev_plan_brief` and stores the brief pointers in `spec`/`metadata`.

---

## Available Commands

### Development

| Command | Description |
|---|---|
| `make dev` | Start all services (docker-up, migrate, web/api/worker) |
| `make dev-web` | Start Next.js dev server |
| `make dev-api` | Start Go API (with Air hot reload if available) |
| `make dev-worker` | Start Go worker |
| `make dev-runner` | Start Go runner service |

### Docker

| Command | Description |
|---|---|
| `make docker-up` | Start Docker services (NATS, etc.) |
| `make docker-down` | Stop Docker services |
| `make docker-logs` | Follow all service logs |
| `make docker-status` | Show service status |
| `make docker-down-volumes` | Stop and remove volumes (DESTRUCTIVE) |

### Database

| Command | Description |
|---|---|
| `make migrate` | Run Goose migrations |
| `make db-reset` | Delete DB and recreate (DESTRUCTIVE) |
| `make db-status` | Show migration status |
| `make gen-db` | Generate SQLC typed code |

### Testing

| Command | Description |
|---|---|
| `make test` | Run all Go tests |
| `make test-api` | Run API tests (verbose) |
| `make test-worker` | Run worker tests |
| `make test-race` | Run tests with race detector |
| `make test-coverage` | Generate coverage report |

### Linting & Build

| Command | Description |
|---|---|
| `make lint` | Run all linters (Go + frontend) |
| `make lint-go` | Run Go vet across all modules |
| `make lint-web` | Run npm lint |
| `make build` | Build all binaries + frontend |
| `make clean` | Remove build artifacts |
| `make fmt` | Format all Go code |

### Utilities

| Command | Description |
|---|---|
| `make install-tools` | Install dev tools (Air, Goose, SQLC) |
| `make deps` | Download Go dependencies |
| `make help` | Show this help |

---

## Project Structure

```
ai-dev-control-plane/
|_ Makefile                          # Root build orchestration
|_ docker-compose.yml                # Local services (NATS, Temporal)
|_ .env.example                      # Environment template
|_ go.work                           # Go workspace
|_ go.work.sum
|
|_ apps/
|  |_ web/                           # Next.js 16 frontend
|  |  |_ app/                        # App Router
|  |  |_ components/                 # React components
|  |  |_ lib/                        # Client utilities
|  |  |_ package.json
|  |  |_ next.config.js
|  |  |_ tailwind.config.ts
|  |  |_ tsconfig.json
|  |
|  |_ api/                           # Go control plane API
|  |  |_ cmd/api/main.go
|  |  |_ internal/
|  |  |  |_ server/server.go
|  |  |  |_ handlers/
|  |  |  |_ middleware/
|  |  |  |_ config/config.go
|  |  |  |_ auth/
|  |  |_ go.mod
|  |  |_ go.sum
|  |
|  |_ worker/                        # Go background workers
|  |  |_ cmd/worker/main.go
|  |  |_ internal/
|  |  |_ go.mod
|  |  |_ go.sum
|  |
|  |_ runner/                        # Go sandbox/runtime service
|     |_ cmd/runner/main.go
|     |_ internal/
|     |_ go.mod
|     |_ go.sum
|
|_ packages/
|  |_ db/                            # Database: schema, migrations, SQLC
|  |  |_ schema.sql
|  |  |_ migrations/                 # Goose migrations
|  |  |_ queries/                    # SQLC query files
|  |  |_ sqlc.yaml
|  |  |_ gen/                        # Generated SQLC code
|  |  |_ adapters/                   # DB adapters (SQLite/Postgres)
|  |  |_ db.go                       # Unified DB interface
|  |
|  |_ agents/                        # Agent interfaces, tool definitions
|  |_ runtimes/                      # Runtime provider interface
|  |_ repo-intel/                    # Repo indexing basics
|  |_ events/                        # NATS event schemas + bus
|  |_ models/                        # Shared domain models
|  |_ policies/                      # Permission + policy engine
|  |_ gateway/                       # GitHub, webhook handlers
|
|_ infra/
   |_ docker/
      |_ api.Dockerfile              # API multi-stage build
      |_ worker.Dockerfile           # Worker multi-stage build
      |_ runner.Dockerfile           # Runner with Docker-in-Docker
      |_ web.Dockerfile              # Next.js standalone build
```

---

## Development Guide

### Go Workspace

This project uses Go 1.23 workspaces. The root `go.work` file includes all
modules. To work on a specific module:

```bash
# Sync workspace dependencies
go work sync

# Run a specific module's tests
cd apps/api && go test ./...

# Build a specific binary
cd apps/api && go build -o ../../bin/api cmd/api/main.go
```

### Adding Migrations

```bash
# Create a new migration
cd packages/db && goose -dir migrations create add_users_table sql

# Edit the generated .sql file, then:
make migrate       # Apply migrations
make db-status     # Check status
```

### Generating SQLC Code

After modifying `schema.sql` or query files in `packages/db/queries/`:

```bash
make gen-db
```

### Hot Reload

Install [Air](https://github.com/air-verse/air) for automatic Go server restart
on file changes:

```bash
make install-tools    # Installs Air, Goose, SQLC
make dev-api          # Auto-uses Air if available
```

---

## Environment Variables

All variables are defined in `.env.example`. Key categories:

| Category | Variables | Description |
|---|---|---|
| **Database** | `DATABASE_URL` | SQLite (local) or Postgres (cloud) |
| **Auth** | `JWT_SECRET`, `GITHUB_*` | JWT signing + GitHub OAuth/App |
| **Services** | `NATS_URL`, `TEMPORAL_HOST` | Message bus + workflow engine |
| **Ports** | `PORT`, `WEB_PORT` | Service port bindings |
| **AI** | `BIFROST_URL`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, `GROQ_API_KEY`, `FIREWORKS_API_KEY` | AI provider configuration |
| **Runtime** | `RUNNER_BASE_DIR`, `DOCKER_HOST` | Sandbox settings |
| **Frontend** | `NEXT_PUBLIC_*` | Public frontend config |
| **Features** | `ENABLE_TEMPORAL`, `REQUIRE_RISK_APPROVAL` | Feature toggles |

See `.env.example` for full documentation and default values.

---

## Database Setup

### SQLite (Default -- Local Development)

```bash
# Migrations run automatically with `make dev`
# DB file is created at: ./data/dev.db

# Reset database (DESTRUCTIVE):
make db-reset
```

### PostgreSQL (Cloud/Production)

```bash
# 1. Uncomment postgres service in docker-compose.yml
# 2. Update DATABASE_URL in .env:
DATABASE_URL=postgres://user:pass@localhost:5432/aicp?sslmode=disable
# 3. Start services:
docker-compose up -d postgres
make migrate
```

### Migration Compatibility

Migration files must be compatible with **both SQLite and PostgreSQL**:
- Use standard SQL types (`TEXT`, `INTEGER`, `BOOLEAN`)
- Use `JSONB` in schema (adapters handle SQLite translation)
- Avoid database-specific features in migrations

---

## NATS / JetStream Setup

NATS starts automatically with `make docker-up`. JetStream is enabled for
persistent event streaming.

```bash
# Start NATS
make docker-up

# Check NATS health
curl http://localhost:8222/healthz

# View NATS dashboard
open http://localhost:8222

# Stream logs
make docker-logs-nats
```

Streams are created automatically by the application on startup:
- `AICP_TASKS` -- Task lifecycle events
- `AICP_AGENT_RUNS` -- Agent execution events
- `AICP_AUDIT` -- Audit log events

---

## GitHub App Configuration

### 1. Create a GitHub App

1. Go to **Settings > Developer settings > GitHub Apps > New GitHub App**
2. Fill in the required fields:
   - **GitHub App name**: Your app name
   - **Homepage URL**: `http://localhost:3000`
   - **Webhook URL**: `https://your-ngrok.ngrok.io/webhooks/github`
   - **Webhook secret**: Generate a secure random string

3. Set permissions:
   - **Repository**: Read & Write (code, issues, pull requests)
   - **Commit statuses**: Read & Write
   - **Webhooks**: Read

4. Subscribe to events:
   - Pull request
   - Push
   - Issues
   - Create (branches/tags)

5. Generate a private key and download the `.pem` file

### 2. Configure Environment

```bash
# .env
GITHUB_APP_ID=your-app-id
GITHUB_APP_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----
...
-----END RSA PRIVATE KEY-----"
GITHUB_APP_WEBHOOK_SECRET=your-webhook-secret
GITHUB_CLIENT_ID=your-client-id
GITHUB_CLIENT_SECRET=your-client-secret
GITHUB_TOKEN=your-github-app-installation-or-fine-scoped-token
```

### 3. Install the App

Install the GitHub App on your repositories to start receiving webhooks.

---

## Docker Services

### Available Services

| Service | Image | Port | Purpose |
|---|---|---|---|
| NATS | `nats:2.10-alpine` | 4222, 8222 | Event bus + JetStream |
| Temporal | `temporalio/auto-setup:1.25` | 7233, 8233 | Workflow engine (optional) |

### Operations

```bash
# Start all core services
make docker-up

# Start with Temporal
make docker-up
# In another terminal:
docker-compose --profile temporal up -d

# View logs
make docker-logs

# Stop everything
make docker-down

# Full reset (removes volumes!)
make clean-all
```

---

## Phase Roadmap

### Phase 1: Foundation (Current)
- [x] Monorepo structure with Go workspace
- [x] Next.js frontend shell with CodeMirror 6
- [x] Go API with Chi router + JWT auth
- [x] Database abstraction (SQLite/Postgres)
- [x] Goose migrations + SQLC code generation
- [x] NATS JetStream event bus
- [x] Docker Compose local services
- [x] Multi-stage Dockerfiles
- [x] GitHub integration (OAuth + App webhooks)
- [x] Makefile orchestration

### Phase 2: Agent Runtime
- [x] Docker sandboxed code execution
- [x] Agent tool system (read, edit, test, git)
- [ ] Multi-model provider support
- [x] Spec generation + approval flow
- [x] Branch isolation + workspace management

### Phase 3: Collaboration
- [x] Team management + RBAC
- [x] Review workflows (human-in-the-loop)
- [x] PR auto-creation
- [ ] Merge/deployment gating
- [x] Audit logging

### Phase 4: Integrations
- [ ] Linear ticket sync
- [ ] Slack/Discord bot commands
- [ ] Voice input (whisper)
- [ ] Webhook extensibility
- [ ] Public API + SDK

---

## Contributing

1. **Fork** the repository
2. **Create a branch**: `git checkout -b feat/my-feature`
3. **Make your changes** with tests
4. **Run checks**: `make lint && make test`
5. **Commit**: `git commit -m "feat: add my feature"`
6. **Push**: `git push origin feat/my-feature`
7. **Open a Pull Request**

### Commit Convention

We follow conventional commits:
- `feat:` -- New feature
- `fix:` -- Bug fix
- `docs:` -- Documentation
- `refactor:` -- Code refactoring
- `test:` -- Adding tests
- `chore:` -- Maintenance tasks

---

## License

MIT License. See [LICENSE](LICENSE) for details.

---

## Support

- **Issues**: [GitHub Issues](https://github.com/your-org/ai-dev-control-plane/issues)
- **Discussions**: [GitHub Discussions](https://github.com/your-org/ai-dev-control-plane/discussions)
- **Documentation**: See `/docs` directory

---

<p align="center">
  Built with Go + Next.js + NATS
</p>
