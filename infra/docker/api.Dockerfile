# AI Dev Control Plane -- unified API + Vite UI image
# ==================================================
# Build:
#   docker build -f infra/docker/api.Dockerfile -t dev-plane-api .
# Run:
#   docker run -p 8080:8080 --env-file .env dev-plane-api

# ------------------------------------------------------------------------------
# Stage 1: Build the TypeScript SDK and Vite UI.
# ------------------------------------------------------------------------------
FROM node:24-alpine AS web-builder

WORKDIR /build

COPY packages/sdk/typescript/package.json packages/sdk/typescript/package-lock.json ./packages/sdk/typescript/
RUN cd packages/sdk/typescript && npm ci
COPY packages/sdk/typescript/ ./packages/sdk/typescript/
RUN cd packages/sdk/typescript && npm run build

COPY apps/web/package.json apps/web/package-lock.json apps/web/.npmrc ./apps/web/
RUN cd apps/web && npm ci
COPY apps/web/ ./apps/web/
RUN mkdir -p apps/api/internal/webui/dist && \
    cd apps/web && npm run build

# ------------------------------------------------------------------------------
# Stage 2: Build the Go binary with SQLite/CGO support and the Vite bundle.
# ------------------------------------------------------------------------------
FROM golang:1.26-alpine AS go-builder

RUN apk add --no-cache build-base git ca-certificates tzdata

WORKDIR /build
COPY . .
COPY --from=web-builder /build/apps/api/internal/webui/dist/web ./apps/api/internal/webui/dist/web

RUN cd apps/api && \
    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    go build \
      -ldflags="-s -w" \
      -o /out/api \
      ./cmd/api

# ------------------------------------------------------------------------------
# Stage 3: Minimal runtime image.
# ------------------------------------------------------------------------------
FROM alpine:latest

RUN apk add --no-cache ca-certificates curl libgcc

RUN addgroup -g 1000 -S aicp && \
    adduser -u 1000 -S aicp -G aicp

WORKDIR /app
COPY --from=go-builder /out/api ./api
RUN chown -R aicp:aicp /app

USER aicp
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -fsS http://localhost:8080/health || exit 1

ENTRYPOINT ["./api"]
