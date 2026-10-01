# Dev Plane CI/automation adapter.
#
# This image intentionally contains only the Dev Plane CLI plus the Git/CA
# tooling required to validate and stage an already-authenticated checkout.
# Repository build toolchains belong in the selected runtime workspace, not in
# this forge-facing adapter image.
FROM golang:1.26.8-alpine AS builder

RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY . .

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags='-s -w' \
    -o /out/dev-plane \
    ./apps/cli/cmd/dev-plane

FROM alpine:3.22

RUN apk add --no-cache ca-certificates git
COPY --from=builder /out/dev-plane /usr/local/bin/dev-plane

ENTRYPOINT ["/usr/local/bin/dev-plane"]
