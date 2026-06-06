# AI Dev Control Plane -- API Service Dockerfile
# ===============================================
# Multi-stage build for the Go API server.
#
# Build:
#   docker build -f infra/docker/api.Dockerfile -t aicp-api .
# Run:
#   docker run -p 8080:8080 --env-file .env aicp-api

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
COPY apps/api/go.mod apps/api/go.sum ./apps/api/
COPY packages/db/go.mod packages/db/go.sum ./packages/db/
COPY packages/models/go.mod packages/models/go.sum ./packages/models/
COPY packages/events/go.mod packages/events/go.sum ./packages/events/
COPY packages/policies/go.mod packages/policies/go.sum ./packages/policies/
COPY packages/gateway/go.mod packages/gateway/go.sum ./packages/gateway/
COPY packages/agents/go.mod packages/agents/go.sum ./packages/agents/
COPY packages/runtimes/go.mod packages/runtimes/go.sum ./packages/runtimes/
COPY packages/repo-intel/go.mod packages/repo-intel/go.sum ./packages/repo-intel/

# Download dependencies
RUN go work sync

# Copy source code
COPY apps/api/ ./apps/api/
COPY packages/ ./packages/
COPY bin/ ./bin/

# Build the binary with optimized settings
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-s -w \
    -X 'main.version=$(git describe --tags --always 2>/dev/null || echo dev)' \
    -X 'main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
    -a -installsuffix cgo \
    -o bin/api \
    ./apps/api/cmd/api/main.go

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
COPY --from=builder /build/bin/api ./api

# Copy any static assets (if needed)
# COPY --from=builder /build/apps/api/static ./static

# Change ownership to non-root user
RUN chown -R aicp:aicp /app

# Switch to non-root user
USER aicp

# Expose API port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the API server
ENTRYPOINT ["./api"]
