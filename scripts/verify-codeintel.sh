#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

printf '[codeintel] python harness tests\n'
python3 scripts/test_codeintel_bakeoff.py

printf '[codeintel] provenance tests\n'
python3 scripts/test_codeintel_provenance.py

printf '[codeintel] repo-intel Go tests\n'
(
  cd packages/repo-intel
  go test ./...
)

if [[ "${CODEINTEL_RUN_LIVE:-0}" == "1" ]]; then
  printf '[codeintel] live backend bake-off\n'
  set +e
  python3 scripts/codeintel_bakeoff.py "$@"
  bakeoff_status=$?
  set -e

  provenance_status=1
  promotion_status=1
  if [[ -f data/codeintel-bakeoff/suite.json ]]; then
    printf '[codeintel] capture provenance\n'
    set +e
    python3 scripts/codeintel_provenance.py data/codeintel-bakeoff/suite.json
    provenance_status=$?
    set -e

    printf '[codeintel] promotion verdict\n'
    set +e
    (
      cd packages/repo-intel
      go run ./cmd/codeintel-promote ../../data/codeintel-bakeoff/suite.json
    )
    promotion_status=$?
    set -e
  else
    printf '[codeintel] provenance/promotion verdict unavailable: suite.json was not produced\n' >&2
  fi

  if [[ $bakeoff_status -ne 0 || $provenance_status -ne 0 || $promotion_status -ne 0 ]]; then
    exit 1
  fi
else
  printf '[codeintel] live bake-off skipped (set CODEINTEL_RUN_LIVE=1 to enable)\n'
fi
