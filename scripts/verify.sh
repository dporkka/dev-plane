#!/usr/bin/env bash
set -euo pipefail

profile="${1:-standard}"

run_static() {
  echo "[verify] static: Go lint/vet, web lint+typecheck, SDK typecheck"
  make lint-go
  make lint-web
  (cd apps/web && npm run typecheck)
  make lint-sdk
}

run_test() {
  echo "[verify] test: Go/SDK tests and web tests"
  make test
  (cd apps/web && npm test)
}

run_build() {
  echo "[verify] build: production builds"
  make build
}

run_standard() {
  run_static
  run_test
  run_build
}

usage() {
  cat <<'EOF'
Usage: bash scripts/verify.sh [profile]

Profiles:
  static       Go lint/vet, web lint + typecheck, SDK typecheck
  test         Go/SDK tests plus web tests
  build        Production builds
  standard     static + test + build (normal pre-PR contract)
  race         standard + Go race tests
  integration  race + fixture/credential-backed integration tests
  live         integration + live end-to-end gates

The script intentionally delegates to checked-in Make/npm commands so local agent
verification and CI exercise the same repository-native behavior.
EOF
}

case "$profile" in
  static)
    run_static
    ;;
  test)
    run_test
    ;;
  build)
    run_build
    ;;
  standard)
    run_standard
    ;;
  race)
    run_standard
    make test-race
    ;;
  integration)
    run_standard
    make test-race
    make integration-test
    ;;
  live)
    run_standard
    make test-race
    make integration-test
    make live-e2e
    ;;
  -h|--help|help)
    usage
    ;;
  *)
    echo "unknown verification profile: $profile" >&2
    usage >&2
    exit 2
    ;;
esac
