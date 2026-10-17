#!/usr/bin/env bash
set -euo pipefail

: "${LINT_BASE_REV:?LINT_BASE_REV must identify a real base commit}"
git cat-file -e "${LINT_BASE_REV}^{commit}"

source_file="packages/agent-runtime/manager.go"
original="$(mktemp)"
output="$(mktemp)"
cp "$source_file" "$original"
cleanup() {
  cp "$original" "$source_file"
  rm -f "$original" "$output"
}
trap cleanup EXIT

# Real, compilable Go code with a new ignored error return. This must produce
# an errcheck finding in the PR diff, not merely a compilation failure.
printf '\nfunc ciLintRatchetProbe() { fmt.Errorf("ci-lint-ratchet-probe") }\n' >> "$source_file"
status=0
(
  cd packages/agent-runtime
  golangci-lint run --config "$GITHUB_WORKSPACE/.golangci.yml" --new-from-rev="$LINT_BASE_REV" --whole-files ./...
) >"$output" 2>&1 || status=$?

if [ "$status" -eq 0 ] || ! grep -Fq 'ci-lint-ratchet-probe' "$output"; then
  cat "$output"
  echo "Incremental lint did not report the introduced ignored error return." >&2
  exit 1
fi
echo "Incremental lint negative control passed."
