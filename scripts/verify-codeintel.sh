#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

printf '[codeintel] python harness tests\n'
python3 scripts/test_codeintel_bakeoff.py

printf '[codeintel] repo-intel Go tests\n'
(
  cd packages/repo-intel
  go test ./...
)

if [[ "${CODEINTEL_RUN_LIVE:-0}" == "1" ]]; then
  printf '[codeintel] live backend bake-off\n'
  python3 scripts/codeintel_bakeoff.py "$@"

  printf '[codeintel] promotion verdict\n'
  (
    cd packages/repo-intel
    go run ./cmd/codeintel-promote ../../data/codeintel-bakeoff/suite.json
  )
else
  printf '[codeintel] live bake-off skipped (set CODEINTEL_RUN_LIVE=1 to enable)\n'
fi
