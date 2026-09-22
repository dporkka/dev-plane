# AI Dev Control Plane — Root Makefile
# ======================================
# Cross-platform (Linux/macOS) build orchestration for local development.
# Uses docker-compose v1 syntax for maximum compatibility.

# --- Phony targets -----------------------------------------------------------
.PHONY: dev dev-web dev-api dev-worker dev-runner         docker-up docker-down docker-logs docker-status         migrate db-reset gen-db         test test-api test-cli test-packages test-race test-sdk         lint lint-go lint-web lint-fix-web lint-sdk         build build-api build-cli build-worker build-runner build-web build-sdk         clean help install-tools

# --- Variables ---------------------------------------------------------------

# Load local .env file if present so app defaults match the project config
ifneq (,$(wildcard ./.env))
    include .env
    export
endif

# Detect OS for cross-platform compatibility
UNAME_S := $(shell uname -s)

# Default environment variables (override in .env or shell)
export DATABASE_URL    ?= file:./data/dev.db?_journal_mode=WAL
export NATS_URL        ?= nats://localhost:4222
export PORT            ?= 8080
export WEB_PORT        ?= 3000
export RUNNER_PORT     ?= 8082
export JWT_SECRET      ?= dev-secret-change-me-min-32-chars-long
export LOG_LEVEL       ?= info
export TEMPORAL_HOST   ?= localhost:7233
export BIFROST_URL     ?= http://localhost:8083
export WORKSPACE_BASE_DIR ?= ./data/workspaces

# Directories
DATA_DIR         := ./data
MIGRATIONS_DIR   := ./packages/db/migrations
BIN_DIR          := ./bin

# Docker Compose command (prefer v2, fall back to v1)
DOCKER_COMPOSE   := $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")

# Derive Go modules from the workspace; fall back to explicit lists if introspection fails.
GO_MODULES       := $(shell go list -f '{{.Dir}}' -m all 2>/dev/null | sed -n 's|$(CURDIR)/||p' | grep -E '^(packages|apps)/')
GO_PACKAGES      := $(filter packages/%,$(GO_MODULES))
GO_APPS          := $(filter apps/%,$(GO_MODULES))

# Fallback explicit lists (used when workspace introspection is unavailable).
ifeq ($(GO_PACKAGES),)
GO_PACKAGES      := packages/agents packages/artifacts packages/crypto packages/db packages/events packages/gateway packages/models packages/policies packages/prfactory packages/repo-intel packages/reviewer packages/runtimes packages/securityscan packages/vcs
endif
ifeq ($(GO_APPS),)
GO_APPS          := apps/api apps/worker apps/runner
endif

# Colors for output (Linux/macOS compatible)
BLUE  := $(shell tput setaf 6 2>/dev/null || echo "")
GREEN := $(shell tput setaf 2 2>/dev/null || echo "")
RESET := $(shell tput sgr0 2>/dev/null || echo "")

# --- Development targets -----------------------------------------------------

dev: ## Start all services (docker-up, migrate, then web/api/worker in parallel)
	@echo "$(GREEN)Starting AI Dev Control Plane in dev mode...$(RESET)"
	@$(MAKE) docker-up
	@echo "$(BLUE)Waiting for NATS to be ready...$(RESET)"
	@i=0; 	while [ $$i -lt 30 ]; do 		if curl -fsS http://localhost:8222/healthz >/dev/null 2>&1 || 		   wget --spider -q http://localhost:8222/healthz >/dev/null 2>&1; then 			echo "$(GREEN)NATS is ready.$(RESET)"; 			break; 		fi; 		i=$$((i + 1)); 		if [ $$i -eq 30 ]; then 			echo "$(BLUE)NATS health check timed out after 30s; continuing anyway...$(RESET)"; 		fi; 		sleep 1; 	done
	@mkdir -p $(DATA_DIR)
	@$(MAKE) migrate
	@echo "$(GREEN)All dependencies ready. Starting applications...$(RESET)"
	@trap 'echo "$(BLUE)Shutting down dev servers...$(RESET)"; kill %1 %2 %3 %4 2>/dev/null; wait' EXIT INT TERM; 		$(MAKE) dev-web & 		$(MAKE) dev-api & 		$(MAKE) dev-worker & 		$(MAKE) dev-runner & 		wait

dev-web: ## Start Next.js dev server on port $(WEB_PORT)
	@echo "$(BLUE)[web]$(RESET) Starting Next.js dev server on port $(WEB_PORT)..."
	cd apps/web && PORT=$(WEB_PORT) npm run dev

dev-api: ## Start Go API server (with hot reload via Air if available)
	@echo "$(BLUE)[api]$(RESET) Starting Go API server on port $(PORT)..."
ifeq ($(shell which air 2>/dev/null),)
	cd apps/api && go run cmd/api/main.go
else
	cd apps/api && air -c .air.toml
endif

dev-worker: ## Start Go worker service
	@echo "$(BLUE)[worker]$(RESET) Starting Go worker..."
	cd apps/worker && go run cmd/worker/main.go

dev-runner: ## Start Go runner (runtime) service on port $(RUNNER_PORT)
	@echo "$(BLUE)[runner]$(RESET) Starting Go runner service on port $(RUNNER_PORT)..."
	cd apps/runner && RUNNER_PORT=$(RUNNER_PORT) go run cmd/runner/main.go

# --- Docker service targets --------------------------------------------------

docker-up: ## Start Docker services (NATS, optional Temporal)
	@echo "$(BLUE)[docker]$(RESET) Starting Docker services..."
	$(DOCKER_COMPOSE) up -d

docker-down: ## Stop Docker services
	@echo "$(BLUE)[docker]$(RESET) Stopping Docker services..."
	$(DOCKER_COMPOSE) down

docker-logs: ## Follow all service logs
	$(DOCKER_COMPOSE) logs -f

docker-status: ## Show service status
	$(DOCKER_COMPOSE) ps

docker-logs-nats: ## Follow NATS logs only
	$(DOCKER_COMPOSE) logs -f nats

docker-logs-temporal: ## Follow Temporal logs only
	$(DOCKER_COMPOSE) logs -f temporal

docker-down-volumes: ## Stop services and remove volumes (DESTRUCTIVE)
	@echo "$(BLUE)[docker]$(RESET) Stopping services and removing volumes..."
	$(DOCKER_COMPOSE) down -v

# --- Database targets --------------------------------------------------------

migrate: ## Run Goose migrations
	@echo "$(BLUE)[db]$(RESET) Running migrations..."
	@mkdir -p $(DATA_DIR)
	goose -dir $(MIGRATIONS_DIR) sqlite3 "$(DATABASE_URL)" up

db-reset: ## Delete DB files and recreate (DESTRUCTIVE)
	@echo "$(BLUE)[db]$(RESET) Resetting database..."
	@rm -f $(DATA_DIR)/dev.db $(DATA_DIR)/dev.db-shm $(DATA_DIR)/dev.db-wal
	@mkdir -p $(DATA_DIR)
	@$(MAKE) migrate
	@echo "$(GREEN)Database reset complete.$(RESET)"

db-status: ## Show migration status
	@goose -dir $(MIGRATIONS_DIR) sqlite3 "$(DATABASE_URL)" status

db-version: ## Show current migration version
	@goose -dir $(MIGRATIONS_DIR) sqlite3 "$(DATABASE_URL)" version

# --- Code generation targets -------------------------------------------------

gen-db: ## Generate SQLC typed code
	@echo "$(BLUE)[gen]$(RESET) Generating SQLC code..."
	cd packages/db && sqlc generate
	@echo "$(GREEN)SQLC generation complete.$(RESET)"

gen-mock: ## Generate Go mocks (if mockgen is installed)
	@echo "$(BLUE)[gen]$(RESET) Generating mocks..."
	go generate ./...

# --- Testing targets ---------------------------------------------------------

test: ## Run all Go tests across packages/apps plus CLI and SDK
	@echo "$(GREEN)Running all tests...$(RESET)"
	@for pkg in $(GO_PACKAGES); do 		echo "$(BLUE)[test]$(RESET) $$pkg"; 		cd $$pkg && go test ./... && cd - > /dev/null || exit 1; 	done
	@for app in $(GO_APPS); do 		echo "$(BLUE)[test]$(RESET) $$app"; 		cd $$app && go test ./... && cd - > /dev/null || exit 1; 	done
	@$(MAKE) test-cli
	@$(MAKE) test-sdk
	@echo "$(GREEN)All tests passed.$(RESET)"

test-api: ## Run API tests (verbose)
	@echo "$(BLUE)[test]$(RESET) Running API tests..."
	cd apps/api && go test ./... -v

test-cli: ## Run CLI tests (verbose)
	@echo "$(BLUE)[test]$(RESET) Running CLI tests..."
	cd apps/cli && go test ./... -v

test-worker: ## Run worker tests (verbose)
	@echo "$(BLUE)[test]$(RESET) Running worker tests..."
	cd apps/worker && go test ./... -v

test-packages: ## Run package tests only
	@echo "$(GREEN)Running package tests...$(RESET)"
	@for pkg in $(GO_PACKAGES); do 		echo "$(BLUE)[test]$(RESET) $$pkg"; 		cd $$pkg && go test ./... && cd - > /dev/null || exit 1; 	done

test-race: ## Run tests with race detector
	@for pkg in $(GO_PACKAGES); do 		echo "$(BLUE)[test]$(RESET) $$pkg"; 		cd $$pkg && go test ./... -race && cd - > /dev/null || exit 1; 	done
	@for app in $(GO_APPS); do 		echo "$(BLUE)[test]$(RESET) $$app"; 		cd $$app && go test ./... -race && cd - > /dev/null || exit 1; 	done

test-sdk: ## Install deps and test TypeScript SDK (typecheck + tests)
	@echo "$(BLUE)[test]$(RESET) Installing and testing TypeScript SDK..."
	cd packages/sdk/typescript && npm ci && npm run typecheck && npm test
	@echo "$(GREEN)SDK tests passed.$(RESET)"
