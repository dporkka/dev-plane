# Local Development Guide

## Prerequisites

- **Go 1.25+** - Backend runtime
- **Node.js 20+** - Frontend runtime
- **Docker + Docker Compose** - Container runtime for workspaces
- **Git** - Version control
- **Make** - Build automation

## Quick Start

```bash
# Clone the repository
git clone <repo-url>
cd dev-plane

# Copy environment file
cp .env.example .env

# Start core Docker services and all apps (API, Web, Worker, Runner)
make dev
```

The development stack will be available at:
- **Web UI**: http://localhost:3000
- **API**: http://localhost:8080
- **Runner**: http://localhost:8082
- **NATS UI**: http://localhost:8222

Optional: start Temporal in another terminal:
```bash
docker compose --profile temporal up -d
# UI: http://localhost:8233
```

## Available Commands

| Make Target | Description |
|-------------|-------------|
| `make dev` | Start Docker services and all apps (web, api, worker, runner) |
| `make dev-web` | Start Next.js dev server |
| `make dev-api` | Start Go API server (uses Air if installed) |
| `make dev-worker` | Start Go worker service |
| `make dev-runner` | Start Go runner service |
| `make build` | Build all binaries and the Next.js production bundle |
| `make test` | Run all Go tests |
| `make test-api` | Run API tests (verbose) |
| `make test-worker` | Run worker tests |
| `make test-packages` | Run package tests only |
| `make test-race` | Run Go tests with race detector |
| `make integration-test` | Run credential-dependent integration tests |
| `make migrate` | Apply database migrations |
| `make db-reset` | Delete DB files (incl. WAL) and re-run migrations |
| `make db-status` | Show migration status |
| `make db-version` | Show current migration version |
| `make gen-db` | Regenerate SQLC code |
| `make gen-mock` | Generate Go mocks |
| `make lint` | Run Go vet + web lint |
| `make lint-fix` | Run auto-fixers |
| `make fmt` | Format Go code |
| `make clean` | Remove build artifacts |
| `make clean-all` | Remove build artifacts and Docker volumes |
| `make docker-up` | Start Docker services |
| `make docker-down` | Stop Docker services |
| `make docker-logs` | Follow Docker service logs |
| `make install-tools` | Install Air, Goose, SQLC |

## Database

### SQLite (Default for Local Dev)

The API server automatically creates a SQLite database at `./data/dev.db` on startup. No additional setup is required.

### Migration Management

Migrations are located in `packages/db/migrations/` and use Goose:

```bash
# Apply all pending migrations
make migrate

# Rollback one migration (not exposed as a make target)
goose -dir packages/db/migrations sqlite3 "$DATABASE_URL" down

# Create a new migration
cd packages/db && goose -dir migrations create add_user_preferences sql

# Check migration status
make db-status
```

### Schema Regeneration

After modifying `packages/db/schema.sql` or `packages/db/queries/`, regenerate the Go code:

```bash
make gen-db
# or
cd packages/db && sqlc generate
```

### Reset Database

```bash
# Removes dev.db plus WAL/SHM files and re-runs migrations
make db-reset
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `file:./data/dev.db?_journal_mode=WAL` | Database connection string |
| `NATS_URL` | `nats://localhost:4222` | NATS server URL |
| `PORT` | `8080` | API server port |
| `WEB_PORT` | `3000` | Next.js dev server port |
| `RUNNER_PORT` | `8082` | Runner service port |
| `JWT_SECRET` | *(required, ≥32 chars)* | JWT signing secret |
| `ALLOWED_ORIGINS` | `http://localhost:3000` | CORS origins |
| `OAUTH_COOKIE_SECURE` | `true` | Set `false` for local HTTP dev |
| `LOG_LEVEL` | `info` | Log verbosity |
| `SECRET_ENCRYPTION_KEYS` | `` | Comma-separated `key-id:base64-32-byte-key` specs |
| `GITHUB_CLIENT_ID` | `` | GitHub OAuth app client ID |
| `GITHUB_CLIENT_SECRET` | `` | GitHub OAuth app secret |
| `GITHUB_TOKEN` | `` | Token used by PR factory for branch push/PR creation |
| `GITHUB_APP_PRIVATE_KEY` | `` | GitHub App private key (PEM) |
| `GITHUB_APP_WEBHOOK_SECRET` | `` | Required; API rejects GitHub webhooks when unset |
| `LINEAR_WEBHOOK_SECRET` | `` | Linear webhook signing secret |
| `SLACK_SIGNING_SECRET` | `` | Slack request signing secret |
| `DISCORD_WEBHOOK_SECRET` | `` | Discord webhook authorization secret |
| `WORKSPACE_RUNTIME` | `local` (code fallback) / `docker` (`.env.example`) | Runtime provider: `local` or `docker` |
| `WORKSPACE_BASE_DIR` | `./data/workspaces` under `make dev` | Base directory for workspaces |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker daemon socket path |
| `DOCKER_WORKSPACE_IMAGE` | `alpine/git:latest` | Docker workspace image |
| `DOCKER_WORKSPACE_MEMORY` | `512m` | Docker memory limit |
| `DOCKER_WORKSPACE_CPUS` | `1.0` | Docker CPU limit |
| `DOCKER_WORKSPACE_PIDS` | `256` | Docker PID limit |
| `RUNNER_URL` | `` | Remote runner endpoint; empty = in-process runtime |
| `RUNNER_AUTH_TOKEN` | `` | Shared secret for remote runner calls |
| `WORKER_HEALTH_PORT` | `8081` | Worker HTTP health endpoint port (container health checks) |
| `NEXT_PUBLIC_API_URL` | `http://localhost:8080` | Frontend API URL |
| `NEXT_PUBLIC_GITHUB_CLIENT_ID` | `` | Public GitHub OAuth client ID |
| `BIFROST_URL` | `http://localhost:8083` | Bifrost AI gateway URL |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` / `GROQ_API_KEY` / `FIREWORKS_API_KEY` | `` | Direct model provider keys |
| `DEFAULT_MODEL` / `DEFAULT_PROVIDER` | `gpt-4o` / `openai` | Default model for agent runs |

## Service Architecture (Dev Mode)

```
localhost:3000  ->  Next.js dev server (hot reload)
localhost:8080  ->  Go API server (auto-restart on change)
localhost:8081  ->  Go Worker health endpoint
localhost:8082  ->  Go Runner service (workspace runtime)
localhost:8083  ->  Bifrost AI Gateway (external; optional)
localhost:4222  ->  NATS JetStream
localhost:8222  ->  NATS monitoring UI
localhost:8233  ->  Temporal Web UI (optional)

## Debugging

### API Server

```bash
# Run with debugger
cd apps/api && dlv debug ./cmd/api

# Or with verbose logging
LOG_LEVEL=debug make dev-api
```

### Web Frontend

```bash
# Run Next.js with full debug logging
cd apps/web && DEBUG=* npm run dev
```

### NATS Debugging

```bash
# Subscribe to all events
nats sub '>'

# Subscribe to task events only
nats sub 'tasks.>'

# Stream info
nats stream info TASKS
```

## Troubleshooting

### Port Already in Use

```bash
# Find and kill processes on required ports
lsof -ti:3000,8080,8082,4222 | xargs kill -9
```

### Docker Permission Issues

```bash
# Add user to docker group
sudo usermod -aG docker $USER
# Log out and back in
```

### SQLite Locked

```bash
# Kill any processes holding the lock
fuser -k ./data/dev.db
```

### NATS Connection Refused

```bash
# Restart NATS container
docker restart aicp-nats
# Or check status
docker logs aicp-nats
```

### Frontend Build Errors

```bash
# Clear Next.js cache
rm -rf apps/web/.next

# Reinstall dependencies
cd apps/web
rm -rf .next node_modules package-lock.json
npm install
```

### Hot Reload Not Working

Ensure your filesystem supports inotify. On WSL2 or Docker volumes, you may need to increase the watch limit:

```bash
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```
