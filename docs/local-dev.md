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
cd ai-dev-control-plane

# Copy environment file
cp .env.example .env

# Start all services (API, Web, NATS, Worker)
make dev
```

The development stack will be available at:
- **Web UI**: http://localhost:3000
- **API**: http://localhost:8080
- **NATS UI**: http://localhost:8222

## Available Commands

| Make Target | Description |
|-------------|-------------|
| `make dev` | Start all services in development mode |
| `make build` | Build all binaries |
| `make test` | Run all tests |
| `make test-unit` | Run unit tests only |
| `make test-integration` | Run integration tests |
| `make migrate-up` | Apply database migrations |
| `make migrate-down` | Rollback last migration |
| `make migrate-create` | Create a new migration |
| `make generate` | Regenerate SQLC queries |
| `make lint` | Run linters on all code |
| `make fmt` | Format all code |
| `make clean` | Clean build artifacts |
| `make docker-build` | Build Docker images |
| `make docker-push` | Push Docker images |

## Database

### SQLite (Default for Local Dev)

The API server automatically creates a SQLite database at `./data/dev.db` on startup. No additional setup is required.

### Migration Management

Migrations are located in `packages/db/migrations/` and use Goose:

```bash
# Apply all pending migrations
make migrate-up

# Rollback one migration
make migrate-down

# Create a new migration
make migrate-create NAME=add_user_preferences

# Check migration status
cd packages/db && goose sqlite3 ./data/dev.db status
```

### Schema Regeneration

After modifying SQL files in `packages/db/queries/`, regenerate the Go code:

```bash
make generate
# or
cd packages/db && sqlc generate
```

### Reset Database

```bash
# Remove SQLite database
rm -f ./data/dev.db

# Re-run migrations
make migrate-up
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `file:./data/dev.db` | Database connection string |
| `NATS_URL` | `nats://localhost:4222` | NATS server URL |
| `API_PORT` | `8080` | API server port |
| `API_HOST` | `0.0.0.0` | API server bind address |
| `TEMPORAL_HOST` | `` | Temporal server (empty = local mode) |
| `GITHUB_APP_ID` | `` | GitHub App ID for integrations |
| `GITHUB_PRIVATE_KEY` | `` | GitHub App private key path |
| `GITHUB_TOKEN` | `` | Token used by PR factory for non-interactive branch push and GitHub PR creation |
| `WEB_URL` | `http://localhost:3000` | Web frontend URL |
| `API_URL` | `http://localhost:8080` | API base URL |
| `LOG_LEVEL` | `info` | Log verbosity (debug/info/warn/error) |
| `SECRET_ENCRYPTION_KEYS` | `` | Comma-separated `key-id:base64-32-byte-key` specs for encrypted secret storage. First key encrypts new values. |
| `GITHUB_APP_WEBHOOK_SECRET` | `` | Shared secret used to verify `X-Hub-Signature-256` on GitHub webhook deliveries. The API rejects webhook deliveries when unset. |
| `WORKSPACE_RUNTIME` | `local` | Runtime provider: `local` for trusted development, `docker` for containerized workspace provisioning. |
| `WORKSPACE_BASE_DIR` | OS temp dir | Base directory for runtime staging and local worktrees |
| `DOCKER_HOST` | `` | Docker daemon socket path |
| `DOCKER_WORKSPACE_IMAGE` | `alpine/git:latest` | Docker workspace image used by the Docker runtime |
| `DOCKER_WORKSPACE_MEMORY` | `512m` | Docker memory limit for workspace containers |
| `DOCKER_WORKSPACE_CPUS` | `1.0` | Docker CPU quota for workspace containers |
| `DOCKER_WORKSPACE_PIDS` | `256` | Docker PID limit for workspace containers |
| `MAX_WORKSPACE_SIZE_MB` | `500` | Max workspace disk usage |
| `AGENT_TIMEOUT_MINUTES` | `30` | Default agent run timeout |

## Service Architecture (Dev Mode)

```
localhost:3000  ->  Next.js dev server (hot reload)
localhost:8080  ->  Go API server (auto-restart on change)
localhost:4222  ->  NATS JetStream
localhost:8222  ->  NATS monitoring UI
```

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
lsof -ti:3000,8080,4222 | xargs kill -9
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
docker restart nats
# Or check status
docker logs nats
```

### Frontend Build Errors

```bash
# Clear Next.js cache
rm -rf apps/web/.next

# Reinstall dependencies
rm -rf node_modules package-lock.json
npm install
```

### Hot Reload Not Working

Ensure your filesystem supports inotify. On WSL2 or Docker volumes, you may need to increase the watch limit:

```bash
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```
