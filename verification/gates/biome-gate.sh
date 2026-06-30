#!/usr/bin/env bash
#
# /verification/gates/biome-gate.sh
#
# Convenience wrapper that runs Biome format --write and Biome lint on changed
# JavaScript/TypeScript/JSON files.  It is intended to be used as a quick local
# pre-commit / pre-push gate in the verification/gates workspace.
#
# Usage:
#   ./biome-gate.sh [--staged] [--cached]
#
# Environment:
#   PTS_BIOME_CONFIG - path to biome.json (default: ./biome.json)
#

set -euo pipefail

: "${PTS_BIOME_CONFIG:=${PWD}/biome.json}"

STAGED_ONLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --staged|--cached)
      STAGED_ONLY=1
      ;;
    -h|--help)
      sed -n '2,12p' "$0"
      exit 0
      ;;
    *)
      echo "[biome-gate] unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift
done

cd "$(git rev-parse --show-toplevel)"

if ! command -v biome &>/dev/null; then
  echo "[biome-gate] biome not installed; skipping" >&2
  exit 0
fi

if [[ "${STAGED_ONLY}" -eq 1 ]]; then
  mapfile -t files < <(git diff --cached --name-only --diff-filter=ACMRT)
else
  mapfile -t files < <(git diff --name-only --diff-filter=ACMRT && git diff --cached --name-only --diff-filter=ACMRT)
fi

if [[ ${#files[@]} -eq 0 ]]; then
  echo "[biome-gate] no changed files"
  exit 0
fi

biome_files=()
for f in "${files[@]}"; do
  # shellcheck disable=SC2221,SC2222
  case "$f" in
    *.js|*.jsx|*.ts|*.tsx|*.mjs|*.cjs|*.json|*.css|*.html)
      biome_files+=("$f")
      ;;
  esac
done

if [[ ${#biome_files[@]} -eq 0 ]]; then
  echo "[biome-gate] no Biome-supported files changed"
  exit 0
fi

biome_args=()
if [[ -f "${PTS_BIOME_CONFIG}" ]]; then
  biome_args+=("--config-path=$(dirname "${PTS_BIOME_CONFIG}")")
elif [[ -d "${PTS_BIOME_CONFIG}" ]]; then
  biome_args+=("--config-path=${PTS_BIOME_CONFIG}")
fi

echo "[biome-gate] running format --write on ${#biome_files[@]} file(s)"
biome format --write "${biome_args[@]}" "${biome_files[@]}"

echo "[biome-gate] running lint on ${#biome_files[@]} file(s)"
biome lint "${biome_args[@]}" "${biome_files[@]}"

echo "[biome-gate] done"
