FROM golang:1.26.8-bookworm AS go-toolchain

FROM node:24-bookworm

ARG CODEBASE_MEMORY_VERSION=0.11.0
ARG GITNEXUS_VERSION=1.6.12

COPY --from=go-toolchain /usr/local/go /usr/local/go

ENV PATH="/usr/local/go/bin:${PATH}" \
    CI=1 \
    GITNEXUS_NO_UPDATE_NOTIFIER=1 \
    GITNEXUS_SKIP_OPTIONAL_GRAMMARS=1 \
    HOME=/var/lib/codeintel \
    GITNEXUS_STORAGE_ROOT=/var/lib/codeintel/gitnexus

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        git \
        python3 \
    && rm -rf /var/lib/apt/lists/*

RUN npm install --global \
        "codebase-memory-mcp@${CODEBASE_MEMORY_VERSION}" \
        "gitnexus@${GITNEXUS_VERSION}"

RUN mkdir -p /var/lib/codeintel/gitnexus /workspace/dev-plane /repos \
    && codebase-memory-mcp --version \
    && gitnexus --version \
    && go version \
    && node --version \
    && python3 --version

WORKDIR /workspace/dev-plane

CMD ["bash", "scripts/verify-codeintel.sh"]
