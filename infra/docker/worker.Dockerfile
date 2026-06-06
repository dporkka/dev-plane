# AI Dev Control Plane -- Worker Service Dockerfile
# ==================================================
# Multi-stage build for the Go background worker.
#
# Build:
#   docker build -f infra/docker/worker.Dockerfile -t aicp-worker .
# Run:
#   docker run --env-file .env aicp-worker

# ------------------------------------------------------------------------------
# Stage 1: Build
# ------------------------------------------------------------------------------
FROM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /build

# Copy go workspace files first for better layer caching
COPY go.work go.work.sum ./
COPY apps/worker/go.mod apps/worker/go.sum ./apps/worker/
COPY packages/db/go.mod packages/db/go.sum ./packages/db/
COPY packages/models/go.mod packages/models/go.sum ./packages/models/
COPY packages/events/go.mod packages/events/go.sum ./packages/events/
COPY packages/agents/go.mod packages/agents/go.sum ./packages/agents/
COPY packages/policies/go.mod packages/policies/go.sum ./packages/policies/
COPY packages/runtimes/go.mod packages/runtimes/go.sum ./packages/runtimes/
COPY packages/repo-intel/go.mod packages/repo-intel/go.sum ./packages/repo-intel/

# Download dependencies
RUN go work sync

# Copy source code
COPY apps/worker/ ./apps/worker/
COPY packages/ ./packages/
COPY bin/ ./bin/

# Build the binary with optimized settings
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-s -w \
    -X 'main.version=$(git describe --tags --always 2>/dev/null || echo dev)' \
    -X 'main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
    -a -installsuffix cgo \
    -o bin/worker \
    ./apps/worker/cmd/worker/main.go

# ------------------------------------------------------------------------------
# Stage 2: Runtime
# ------------------------------------------------------------------------------
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates curl

# Create non-root user
RUN addgroup -g 1000 -S aicp && \
    adduser -u 1000 -S aicp -G aicp

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/bin/worker ./worker

# Change ownership to non-root user
RUN chown -R aicp:aicp /app

# Switch to non-root user
USER aicp

# Health check (workers expose a minimal health endpoint)
HEALTHCHECK --interval=60s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8081/health || exit 1

# Run the worker
ENTRYPOINT ["./worker"]
