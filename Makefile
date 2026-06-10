# AI Dev Control Plane — Root Makefile
# ======================================
# Cross-platform (Linux/macOS) build orchestration for local development.
# Uses docker-compose v1 syntax for maximum compatibility.

# --- Phony targets -----------------------------------------------------------
.PHONY: dev dev-web dev-api dev-worker dev-runner \
        docker-up docker-down docker-logs docker-status \
        migrate db-reset gen-db \
        test test-api test-packages test-race \
        lint lint-go lint-web \
        build build-api build-worker build-runner build-web \
        clean help

# --- Variables ---------------------------------------------------------------

# Detect OS for cross-platform compatibility
UNAME_S := $(shell uname -s)

# Default environment variables (override in .env or shell)
export DATABASE_URL    ?= file:./data/dev.db?_journal_mode=WAL
export NATS_URL        ?= nats://localhost:4222
export PORT            ?= 8080
export WEB_PORT        ?= 3000
export JWT_SECRET      ?= dev-secret-change-me
export LOG_LEVEL       ?= info
export TEMPORAL_HOST   ?= localhost:7233
export BIFROST_URL     ?= http://localhost:8081
export RUNNER_BASE_DIR ?= ./data/workspaces

# Directories
DATA_DIR         := ./data
MIGRATIONS_DIR   := ./packages/db/migrations
BIN_DIR          := ./bin

# Docker Compose command (v1 for compatibility)
DOCKER_COMPOSE   := docker-compose

# Go packages with tests
GO_PACKAGES      := packages/db packages/agents packages/runtimes packages/repo-intel packages/events packages/models packages/policies packages/gateway packages/prfactory packages/reviewer packages/securityscan
GO_APPS          := apps/api apps/worker apps/runner

# Colors for output (Linux/macOS compatible)
BLUE  := $(shell tput setaf 6 2>/dev/null || echo "")
GREEN := $(shell tput setaf 2 2>/dev/null || echo "")
RESET := $(shell tput sgr0 2>/dev/null || echo "")

# --- Development targets -----------------------------------------------------

dev: ## Start all services (docker-up, migrate, then web/api/worker in parallel)
	@echo "$(GREEN)Starting AI Dev Control Plane in dev mode...$(RESET)"
	@$(MAKE) docker-up
	@echo "$(BLUE)Waiting for services to be ready...$(RESET)"
	@sleep 3
	@mkdir -p $(DATA_DIR)
	@$(MAKE) migrate
	@echo "$(GREEN)All dependencies ready. Starting applications...$(RESET)"
	@trap 'echo "$(BLUE)Shutting down dev servers...$(RESET)"; kill %1 %2 %3 2>/dev/null; wait' EXIT INT TERM; \
		$(MAKE) dev-web & \
		$(MAKE) dev-api & \
		$(MAKE) dev-worker & \
		wait

dev-web: ## Start Next.js dev server
	@echo "$(BLUE)[web]$(RESET) Starting Next.js dev server on port $(WEB_PORT)..."
	cd apps/web && npm run dev

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

dev-runner: ## Start Go runner (runtime) service
	@echo "$(BLUE)[runner]$(RESET) Starting Go runner service..."
	cd apps/runner && go run cmd/runner/main.go

# --- Docker service targets --------------------------------------------------

docker-up: ## Start Docker services (NATS, optional Temporal)
	@echo "$(BLUE)[docker]$(RESET) Starting Docker services..."
	$(DOCKER_COMPOSE) up -d

docker-down: ## Stop Docker services
	@echo "$(BLUE)[docker]$(RESET) Stopping Docker services..."
	$(DOCKER_COMPOSE) down

docker-logs: ## Follow Docker service logs
	$(DOCKER_COMPOSE) logs -f

docker-status: ## Show Docker service status
	$(DOCKER_COMPOSE) ps

docker-logs-nats: ## Follow NATS logs only
	$(DOCKER_COMPOSE) logs -f nats

docker-logs-temporal: ## Follow Temporal logs only
	$(DOCKER_COMPOSE) logs -f temporal

docker-down-volumes: ## Stop Docker services and remove volumes (DESTRUCTIVE)
	@echo "$(BLUE)[docker]$(RESET) Stopping services and removing volumes..."
	$(DOCKER_COMPOSE) down -v

# --- Database targets --------------------------------------------------------

migrate: ## Run database migrations (Goose)
	@echo "$(BLUE)[db]$(RESET) Running migrations..."
	@mkdir -p $(DATA_DIR)
	cd packages/db && goose -dir $(MIGRATIONS_DIR) sqlite3 "$(DATABASE_URL)" up

db-reset: ## Delete DB files and recreate (DESTRUCTIVE)
	@echo "$(BLUE)[db]$(RESET) Resetting database..."
	@rm -f $(DATA_DIR)/dev.db $(DATA_DIR)/dev.db-shm $(DATA_DIR)/dev.db-wal
	@mkdir -p $(DATA_DIR)
	@$(MAKE) migrate
	@echo "$(GREEN)Database reset complete.$(RESET)"

db-status: ## Show current migration status
	@cd packages/db && goose -dir $(MIGRATIONS_DIR) sqlite3 "$(DATABASE_URL)" status

db-version: ## Show current migration version
	@cd packages/db && goose -dir $(MIGRATIONS_DIR) sqlite3 "$(DATABASE_URL)" version

# --- Code generation targets -------------------------------------------------

gen-db: ## Generate SQLC typed database code
	@echo "$(BLUE)[gen]$(RESET) Generating SQLC code..."
	cd packages/db && sqlc generate
	@echo "$(GREEN)SQLC generation complete.$(RESET)"

gen-mock: ## Generate Go mocks (if mockgen is installed)
	@echo "$(BLUE)[gen]$(RESET) Generating mocks..."
	go generate ./...

# --- Testing targets ---------------------------------------------------------

test: ## Run ALL Go tests across all packages
	@echo "$(GREEN)Running all tests...$(RESET)"
	@for pkg in $(GO_PACKAGES); do \
		echo "$(BLUE)[test]$(RESET) $$pkg"; \
		cd $$pkg && go test ./... && cd - > /dev/null || exit 1; \
	done
	@for app in $(GO_APPS); do \
		echo "$(BLUE)[test]$(RESET) $$app"; \
		cd $$app && go test ./... && cd - > /dev/null || exit 1; \
	done
	@echo "$(GREEN)All tests passed.$(RESET)"

test-api: ## Run API-specific tests with verbose output
	@echo "$(BLUE)[test]$(RESET) Running API tests..."
	cd apps/api && go test ./... -v

test-worker: ## Run worker-specific tests
	@echo "$(BLUE)[test]$(RESET) Running worker tests..."
	cd apps/worker && go test ./... -v

test-packages: ## Run package tests only (no apps)
	@echo "$(GREEN)Running package tests...$(RESET)"
	@for pkg in $(GO_PACKAGES); do \
		echo "$(BLUE)[test]$(RESET) $$pkg"; \
		cd $$pkg && go test ./... && cd - > /dev/null || exit 1; \
	done

test-race: ## Run Go tests with race detector
	@echo "$(GREEN)Running tests with race detector...$(RESET)"
	@for pkg in $(GO_PACKAGES); do \
		echo "$(BLUE)[test]$(RESET) $$pkg"; \
		cd $$pkg && go test ./... -race && cd - > /dev/null || exit 1; \
	done
	@for app in $(GO_APPS); do \
		echo "$(BLUE)[test]$(RESET) $$app"; \
		cd $$app && go test ./... -race && cd - > /dev/null || exit 1; \
	done

test-coverage: ## Run tests with coverage report
	@echo "$(GREEN)Running tests with coverage...$(RESET)"
	@mkdir -p $(BIN_DIR)
	go test ./packages/... ./apps/... -coverprofile=$(BIN_DIR)/coverage.out
	go tool cover -html=$(BIN_DIR)/coverage.out -o $(BIN_DIR)/coverage.html
	@echo "$(GREEN)Coverage report: $(BIN_DIR)/coverage.html$(RESET)"

# --- Linting targets ---------------------------------------------------------

lint: lint-go lint-web ## Run all linters (Go + frontend)

lint-go: ## Run Go vet across all Go modules
	@echo "$(BLUE)[lint]$(RESET) Running Go vet..."
	@for pkg in $(GO_PACKAGES); do \
		echo "  --> $$pkg"; \
		cd $$pkg && go vet ./... && cd - > /dev/null || exit 1; \
	done
	@for app in $(GO_APPS); do \
		echo "  --> $$app"; \
		cd $$app && go vet ./... && cd - > /dev/null || exit 1; \
	done
	@echo "$(GREEN)Go linting complete.$(RESET)"

lint-web: ## Run npm lint in frontend
	@echo "$(BLUE)[lint]$(RESET) Running web lint..."
	cd apps/web && npm run lint

lint-fix: ## Run linters with auto-fix
	@echo "$(BLUE)[lint]$(RESET) Running auto-fix..."
	cd apps/web && npm run lint -- --fix 2>/dev/null || true
	@echo "$(GREEN)Auto-fix complete.$(RESET)"

# --- Build targets -----------------------------------------------------------

build: build-api build-worker build-runner build-web ## Build all binaries and frontend

build-api: ## Build API binary --> bin/api
	@echo "$(BLUE)[build]$(RESET) Building API..."
	@mkdir -p $(BIN_DIR)
	cd apps/api && go build -ldflags="-s -w" -o ../../$(BIN_DIR)/api cmd/api/main.go
	@echo "$(GREEN)API binary: $(BIN_DIR)/api$(RESET)"

build-worker: ## Build worker binary --> bin/worker
	@echo "$(BLUE)[build]$(RESET) Building worker..."
	@mkdir -p $(BIN_DIR)
	cd apps/worker && go build -ldflags="-s -w" -o ../../$(BIN_DIR)/worker cmd/worker/main.go
	@echo "$(GREEN)Worker binary: $(BIN_DIR)/worker$(RESET)"

build-runner: ## Build runner binary --> bin/runner
	@echo "$(BLUE)[build]$(RESET) Building runner..."
	@mkdir -p $(BIN_DIR)
	cd apps/runner && go build -ldflags="-s -w" -o ../../$(BIN_DIR)/runner cmd/runner/main.go
	@echo "$(GREEN)Runner binary: $(BIN_DIR)/runner$(RESET)"

build-web: ## Build Next.js for production
	@echo "$(BLUE)[build]$(RESET) Building Next.js..."
	cd apps/web && npm run build
	@echo "$(GREEN)Next.js build complete.$(RESET)"

# --- Clean targets -----------------------------------------------------------

clean: ## Remove bin/ and .next/ build artifacts
	@echo "$(BLUE)[clean]$(RESET) Removing build artifacts..."
	@rm -rf $(BIN_DIR)/*
	@cd apps/web && rm -rf .next/
	@echo "$(GREEN)Clean complete.$(RESET)"

clean-all: clean ## Remove all artifacts including Docker volumes (DESTRUCTIVE)
	@echo "$(BLUE)[clean]$(RESET) Removing Docker volumes..."
	$(DOCKER_COMPOSE) down -v 2>/dev/null || true
	@rm -rf $(DATA_DIR)/*
	@echo "$(GREEN)Full clean complete.$(RESET)"

# --- Utility targets ---------------------------------------------------------

install-tools: ## Install development tools (Air, Goose, SQLC)
	@echo "$(BLUE)[tools]$(RESET) Installing dev tools..."
	go install github.com/air-verse/air@latest
	go install github.com/pressly/goose/v3/cmd/goose@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	@echo "$(GREEN)Dev tools installed.$(RESET)"

deps: ## Download and verify Go module dependencies
	@echo "$(BLUE)[deps]$(RESET) Downloading Go dependencies..."
	@for pkg in $(GO_PACKAGES); do \
		echo "  --> $$pkg"; \
		cd $$pkg && go mod download && cd - > /dev/null; \
	done
	@for app in $(GO_APPS); do \
		echo "  --> $$app"; \
		cd $$app && go mod download && cd - > /dev/null; \
	done
	@echo "$(GREEN)Dependencies downloaded.$(RESET)"

fmt: ## Format all Go code
	@echo "$(BLUE)[fmt]$(RESET) Formatting Go code..."
	gofmt -w packages/ apps/
	@echo "$(GREEN)Formatting complete.$(RESET)"

# --- Help target -------------------------------------------------------------

help: ## Show this self-documenting help
	@echo ""
	@echo "  $(GREEN)AI Dev Control Plane$(RESET) -- Available Commands"
	@echo "  ============================================"
	@echo ""
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  $(BLUE)%-18s$(RESET) %s\n", $$1, $$2}' | \
		sort
	@echo ""

# Default target
.DEFAULT_GOAL := help
