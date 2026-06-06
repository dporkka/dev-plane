# AI Dev Control Plane -- Runner Service Dockerfile
# ==================================================
# Multi-stage build for the Go runtime runner service.
# Supports Docker-in-Docker for sandboxed code execution.
#
# Build:
#   docker build -f infra/docker/runner.Dockerfile -t aicp-runner .
# Run:
#   docker run -v /var/run/docker.sock:/var/run/docker.sock --env-file .env aicp-runner

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
COPY apps/runner/go.mod apps/runner/go.sum ./apps/runner/
COPY packages/db/go.mod packages/db/go.sum ./packages/db/
COPY packages/models/go.mod packages/models/go.sum ./packages/models/
COPY packages/events/go.mod packages/events/go.sum ./packages/events/
COPY packages/runtimes/go.mod packages/runtimes/go.sum ./packages/runtimes/
COPY packages/agents/go.mod packages/agents/go.sum ./packages/agents/

# Download dependencies
RUN go work sync

# Copy source code
COPY apps/runner/ ./apps/runner/
COPY packages/ ./packages/
COPY bin/ ./bin/

# Build the binary with optimized settings
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-s -w \
    -X 'main.version=$(git describe --tags --always 2>/dev/null || echo dev)' \
    -X 'main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
    -a -installsuffix cgo \
    -o bin/runner \
    ./apps/runner/cmd/runner/main.go

# ------------------------------------------------------------------------------
# Stage 2: Runtime (with Docker support)
# ------------------------------------------------------------------------------
FROM alpine:latest

# Install runtime dependencies:
#   - docker-cli: for Docker-in-Docker sandbox management
#   - curl: for health checks
#   - git: for repository operations
#   - ca-certificates: for TLS connections
RUN apk --no-cache add \
    ca-certificates \
    curl \
    git \
    openssh-client

# Install Docker CLI (for DinD -- mount host socket)
RUN apk --no-cache add docker-cli docker-cli-compose

# Create non-root user
RUN addgroup -g 1000 -S aicp && \
    adduser -u 1000 -S aicp -G aicp

# Create workspace directory for sandboxed runs
RUN mkdir -p /app/workspaces && chown -R aicp:aicp /app/workspaces

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/bin/runner ./runner

# Change ownership
RUN chown -R aicp:aicp /app

# Switch to non-root user
USER aicp

# Expose runner port
EXPOSE 8082

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8082/health || exit 1

# Note: To use Docker-in-Docker, mount the host Docker socket:
#   docker run -v /var/run/docker.sock:/var/run/docker.sock aicp-runner
#
# For Kubernetes, use a Docker sidecar or kaniko/buildah for sandboxed builds.

# Run the runner service
ENTRYPOINT ["./runner"]
