#!/usr/bin/env bash
#
# /verification/gates/pts.sh
#
# Predictive Test Selection (PTS) gate for the Multi-Agent Developer Mesh.
#
# This script implements the verification gate that decides which tests must run
# for a given set of code mutations.  It is designed to run locally on a
# developer workstation as well as in CI before agents are allowed to promote
# changes through the mesh.
#
# Pipeline:
#   1. Discover changed source files (staged, unstaged, or both).
#   2. Run an ast-grep structural scan against the mutations.
#   3. Map mutated files to test targets using, in order of preference:
#      - a historical coverage mapping file, and
#      - language-specific heuristics (Go, TypeScript, JavaScript, etc.).
#   4. Lint the changed files with Biome.
#   5. Check formatting of the changed files with Biome.
#   6. Execute the selected tests.
#   7. If tests fail, enter a self-correction loop that retries up to D turns,
#      captures stderr, publishes it to a WASM actor over NATS, and expands the
#      test selection on each iteration.
#   8. Emit a human-readable report and/or JSON machine output.
#
# Usage:
#   ./verification/gates/pts.sh [--staged] [--unstaged] [--all] [--json]
#                               [--max-retries N] [--coverage-file PATH]
#                               [--ast-grep-config PATH] [--biome-config PATH]
#                               [--test-runner CMD] [--report-dir DIR]
#
# Exit codes:
#   0  - gate passed (lint and format clean and all selected tests passed)
#   1  - gate failed (lint/format errors, test failures, or runtime error)
#   2  - bad arguments or missing required tooling
#
# Environment overrides:
#   PTS_AST_GREP_CONFIG   - path to ast-grep rule config (default: sgconfig.yml)
#   PTS_COVERAGE_FILE     - path to coverage-to-test mapping
#   PTS_BIOME_CONFIG      - path to biome.json
#   PTS_TEST_RUNNER       - test command prefix (default: "go test")
#   PTS_REPORT_DIR        - where to write reports (default: .pts-reports)
#   PTS_MAX_RETRIES       - self-correction turn budget D (default: 5)
#   PTS_OWNER             - owner tag for self-correction feedback (default: mesh)
#   PTS_SESSION_ID        - session id for self-correction feedback
#                           (default: git short HEAD)
#   PTS_NATS_URL          - NATS server URL for self-correction feedback
#   PTS_NATS_CREDS        - NATS credentials file for self-correction feedback
#   PTS_JSON              - set to "1" to enable JSON output
#   PTS_VERBOSE           - set to "1" for debug logging
#

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------

: "${PTS_AST_GREP_CONFIG:=${PWD}/sgconfig.yml}"
: "${PTS_COVERAGE_FILE:=${PWD}/.pts-coverage-map.tsv}"
: "${PTS_BIOME_CONFIG:=${PWD}/biome.json}"
: "${PTS_TEST_RUNNER:=go test}"
: "${PTS_REPORT_DIR:=${PWD}/.pts-reports}"
: "${PTS_MAX_RETRIES:=5}"
: "${PTS_OWNER:=mesh}"
: "${PTS_NATS_URL:=}"
: "${PTS_NATS_CREDS:=}"
: "${PTS_JSON:=0}"
: "${PTS_VERBOSE:=0}"

# Internal state populated during the run.
declare -a CHANGED_FILES=()
declare -a MUTATION_RULES=()
declare -A TEST_TARGETS=()
declare -A COVERAGE_MAP=()
declare -a TEST_LOGS=()
declare LINT_STATUS=0
declare FORMAT_STATUS=0

# Scratch array used by mapping functions to collect targets for the current
# changed file.  We use a global array instead of namerefs to avoid Bash
# circular-name-reference warnings when nested mapping helpers are called.
declare -a __PTS_CURRENT_TARGETS=()

# Captured stderr from the most recent test attempt, used for self-correction
# feedback published to the WASM actor over NATS.
declare TEST_STDERR=""

# Self-correction feedback context.
declare SELF_CORRECT_OWNER="mesh"
declare SELF_CORRECT_SESSION="default"

declare JSON_OUTPUT=0
declare STAGED=0
declare UNSTAGED=0
declare ALL_CHANGES=0
declare MAX_RETRIES="${PTS_MAX_RETRIES}"

# ---------------------------------------------------------------------------
# Logging helpers
# ---------------------------------------------------------------------------

log() {
  if [[ "${JSON_OUTPUT}" -eq 0 ]]; then
    printf '[pts] %s\n' "$*"
  fi
}

log_debug() {
  if [[ "${PTS_VERBOSE}" -eq 1 && "${JSON_OUTPUT}" -eq 0 ]]; then
    printf '[pts][debug] %s\n' "$*" >&2
  fi
}

warn() {
  if [[ "${JSON_OUTPUT}" -eq 0 ]]; then
    printf '[pts][warn] %s\n' "$*" >&2
  fi
}

error() {
  if [[ "${JSON_OUTPUT}" -eq 0 ]]; then
    printf '[pts][error] %s\n' "$*" >&2
  fi
}

# ---------------------------------------------------------------------------
# Usage
# ---------------------------------------------------------------------------

usage() {
  cat <<'EOF'
Usage: pts.sh [OPTIONS]

Predictive test selection gate.

Options:
  --staged              Include staged changes.
  --unstaged            Include unstaged changes.
  --all                 Include both staged and unstaged changes.
  --json                Emit JSON result to stdout instead of human text.
  --max-retries N       Maximum self-correction turns D (default: 5).
  --coverage-file PATH  Historical coverage mapping file.
  --ast-grep-config PATH
                        ast-grep rule configuration file.
  --biome-config PATH   Biome configuration file.
  --test-runner CMD     Test command prefix (default: "go test").
  --report-dir DIR      Directory for written reports.
  -h, --help            Show this help message.

Environment:
  PTS_OWNER             Owner tag for agents.selfcorrect.feedback.{owner}.{session}
  PTS_SESSION_ID        Session id (default: git short HEAD)
  PTS_NATS_URL          NATS server URL for feedback publishing
  PTS_NATS_CREDS        NATS credentials file for feedback publishing

Examples:
  pts.sh --unstaged
  pts.sh --staged --json --max-retries 3
  pts.sh --all --coverage-file coverage.tsv
EOF
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --staged)
        STAGED=1
        ;;
      --unstaged)
        UNSTAGED=1
        ;;
      --all)
        ALL_CHANGES=1
        ;;
      --json)
        JSON_OUTPUT=1
        ;;
      --max-retries)
        shift
        [[ $# -gt 0 ]] || { error "--max-retries requires a value"; exit 2; }
        MAX_RETRIES="$1"
        ;;
      --coverage-file)
        shift
        [[ $# -gt 0 ]] || { error "--coverage-file requires a value"; exit 2; }
        PTS_COVERAGE_FILE="$1"
        ;;
      --ast-grep-config)
        shift
        [[ $# -gt 0 ]] || { error "--ast-grep-config requires a value"; exit 2; }
        PTS_AST_GREP_CONFIG="$1"
        ;;
      --biome-config)
        shift
        [[ $# -gt 0 ]] || { error "--biome-config requires a value"; exit 2; }
        PTS_BIOME_CONFIG="$1"
        ;;
      --test-runner)
        shift
        [[ $# -gt 0 ]] || { error "--test-runner requires a value"; exit 2; }
        PTS_TEST_RUNNER="$1"
        ;;
      --report-dir)
        shift
        [[ $# -gt 0 ]] || { error "--report-dir requires a value"; exit 2; }
        PTS_REPORT_DIR="$1"
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        error "Unknown argument: $1"
        usage >&2
        exit 2
        ;;
    esac
    shift
  done

  # Default to unstaged if no scope selected.
  if [[ "${STAGED}" -eq 0 && "${UNSTAGED}" -eq 0 && "${ALL_CHANGES}" -eq 0 ]]; then
    UNSTAGED=1
  fi

  # ALL implies both.
  if [[ "${ALL_CHANGES}" -eq 1 ]]; then
    STAGED=1
    UNSTAGED=1
  fi

  if ! [[ "${MAX_RETRIES}" =~ ^[0-9]+$ ]]; then
    error "--max-retries must be a non-negative integer"
    exit 2
  fi
}

# ---------------------------------------------------------------------------
# Preconditions
# ---------------------------------------------------------------------------

require_cmds() {
  if ! command -v git &>/dev/null; then
    error "Missing required command: git"
    exit 2
  fi

  if ! command -v python3 &>/dev/null; then
    error "Missing required command: python3 (needed for JSON parsing)"
    exit 2
  fi

  if ! command -v ast-grep &>/dev/null && ! command -v sg &>/dev/null; then
    warn "ast-grep (sg) not found; structural mutation scan will be skipped"
  fi

  if ! command -v biome &>/dev/null; then
    warn "biome not found; lint/format steps will be skipped"
  fi
}

ensure_git_root() {
  local git_root
  git_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
  if [[ -z "${git_root}" ]]; then
    error "Not inside a git repository"
    exit 2
  fi
  cd "${git_root}"
  log_debug "Working from git root: ${git_root}"
}

# ---------------------------------------------------------------------------
# Self-correction feedback context
# ---------------------------------------------------------------------------

init_selfcorrect_context() {
  SELF_CORRECT_OWNER="${PTS_OWNER}"
  SELF_CORRECT_SESSION="${PTS_SESSION_ID:-$(git rev-parse --short HEAD 2>/dev/null || echo 'default')}"
}

# Build the self-correction feedback JSON payload.
build_selfcorrect_payload() {
  local attempt="$1"
  local stderr_payload="$2"

  # Truncate enormous stderr so the NATS message stays well under 1 MiB.
  local truncated
  truncated="${stderr_payload:0:1048576}"

  PTS_FEEDBACK_STDERR="${truncated}" \
  PTS_FEEDBACK_ATTEMPT="${attempt}" \
  PTS_FEEDBACK_OWNER="${SELF_CORRECT_OWNER}" \
  PTS_FEEDBACK_SESSION="${SELF_CORRECT_SESSION}" \
  PTS_FEEDBACK_TARGETS="$(printf '%s\t' "${!TEST_TARGETS[@]}")" \
  python3 - <<'PY'
import json, os, datetime
payload = {
    "stderr": os.environ.get("PTS_FEEDBACK_STDERR", ""),
    "attempt": int(os.environ.get("PTS_FEEDBACK_ATTEMPT", "1")),
    "owner": os.environ.get("PTS_FEEDBACK_OWNER", "mesh"),
    "session": os.environ.get("PTS_FEEDBACK_SESSION", "default"),
    "targets": [t for t in os.environ.get("PTS_FEEDBACK_TARGETS", "").split("\t") if t],
    "timestamp": datetime.datetime.utcnow().isoformat() + "Z"
}
print(json.dumps(payload, ensure_ascii=False))
PY
}

# Publish captured test stderr to the WASM actor over NATS.
# If the nats CLI is unavailable, write the payload to a local file so the
# feedback is not lost. This keeps the gate usable in minimal CI environments.
publish_selfcorrect_feedback() {
  local attempt="$1"
  local stderr_payload="$2"

  local payload
  payload="$(build_selfcorrect_payload "${attempt}" "${stderr_payload}")"

  local subject
  subject="agents.selfcorrect.feedback.${SELF_CORRECT_OWNER}.${SELF_CORRECT_SESSION}"

  if command -v nats &>/dev/null; then
    local nats_args=()
    if [[ -n "${PTS_NATS_URL:-}" ]]; then
      nats_args+=("--server=${PTS_NATS_URL}")
    fi
    if [[ -n "${PTS_NATS_CREDS:-}" ]]; then
      nats_args+=("--creds=${PTS_NATS_CREDS}")
    fi

    log_debug "Publishing self-correction feedback to ${subject}"
    if ! nats pub "${nats_args[@]}" "${subject}" "${payload}" >/dev/null 2>&1; then
      warn "Failed to publish self-correction feedback to ${subject}"
    fi
  else
    log_debug "nats CLI not found; writing self-correction feedback to local file"
    local fallback
    fallback="${PTS_REPORT_DIR}/selfcorrect-feedback-${attempt}-$(date +%s).json"
    printf '%s\n' "${payload}" > "${fallback}"
    log "Self-correction feedback written to ${fallback} (nats CLI unavailable)"
  fi
}

# ---------------------------------------------------------------------------
# Changed file discovery
# ---------------------------------------------------------------------------

discover_changed_files() {
  local -a files=()

  if [[ "${UNSTAGED}" -eq 1 ]]; then
    while IFS= read -r f; do
      [[ -n "${f}" ]] && files+=("${f}")
    done < <(git diff --name-only --diff-filter=ACMRT)
  fi

  if [[ "${STAGED}" -eq 1 ]]; then
    while IFS= read -r f; do
      [[ -n "${f}" ]] && files+=("${f}")
    done < <(git diff --cached --name-only --diff-filter=ACMRT)
  fi

  if [[ ${#files[@]} -eq 0 ]]; then
    warn "No changed files detected in the selected scope"
    return 0
  fi

  # Deduplicate while preserving order.
  local -A seen=()
  for f in "${files[@]}"; do
    if [[ -z "${seen[${f}]:-}" ]]; then
      seen[${f}]=1
      CHANGED_FILES+=("${f}")
    fi
  done

  log "Discovered ${#CHANGED_FILES[@]} changed file(s)"
  for f in "${CHANGED_FILES[@]}"; do
    log_debug "  changed: ${f}"
  done
}

# ---------------------------------------------------------------------------
# Structural mutation scan with ast-grep
# ---------------------------------------------------------------------------

run_ast_grep_scan() {
  local sg_cmd
  if command -v sg &>/dev/null; then
    sg_cmd="sg"
  elif command -v ast-grep &>/dev/null; then
    sg_cmd="ast-grep"
  else
    warn "ast-grep unavailable; skipping structural scan"
    return 0
  fi

  if [[ ! -f "${PTS_AST_GREP_CONFIG}" ]]; then
    warn "ast-grep config not found at ${PTS_AST_GREP_CONFIG}; skipping structural scan"
    return 0
  fi

  log "Running ast-grep structural scan..."

  local scan_output
  local scan_status=0
  scan_output="$(${sg_cmd} scan \
    --config "${PTS_AST_GREP_CONFIG}" \
    --json \
    "${CHANGED_FILES[@]}" 2>/dev/null)" || scan_status=$?

  if [[ "${scan_status}" -ne 0 && -z "${scan_output}" ]]; then
    warn "ast-grep scan returned non-zero and produced no parseable output"
    return 0
  fi

  # ast-grep JSON output may be a single object, a JSON array, or NDJSON.
  # Use Python to robustly extract rule ids / rule names from every match.
  local -a rules=()
  while IFS= read -r rule; do
    [[ -n "${rule}" ]] && rules+=("${rule}")
  done < <(python3 -c '
import json, sys

data = sys.stdin.read().strip()
if not data:
    sys.exit(0)

def emit(item):
    rid = item.get("ruleId") or item.get("rule") or ""
    if rid:
        print(rid)

try:
    obj = json.loads(data)
    if isinstance(obj, list):
        for item in obj:
            emit(item)
    else:
        emit(obj)
except json.JSONDecodeError:
    # NDJSON fallback.
    for line in data.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            emit(json.loads(line))
        except json.JSONDecodeError:
            continue
' <<< "${scan_output}")

  # Deduplicate rules.
  local -A seen=()
  for r in "${rules[@]}"; do
    if [[ -z "${seen[${r}]:-}" ]]; then
      seen[${r}]=1
      MUTATION_RULES+=("${r}")
    fi
  done

  log "Structural scan matched ${#MUTATION_RULES[@]} rule(s)"
  for r in "${MUTATION_RULES[@]}"; do
    log_debug "  rule: ${r}"
  done
}

# ---------------------------------------------------------------------------
# Coverage mapping
#
# Supported formats:
#   TSV  -  source_pattern<TAB>test_target
#   JSON -  {"mappings": [{"pattern": "...", "target": "..."}]}
#
# Patterns are Bash-extended globs matched against changed file paths.
# ---------------------------------------------------------------------------

load_coverage_map() {
  if [[ ! -f "${PTS_COVERAGE_FILE}" ]]; then
    log_debug "No coverage mapping file at ${PTS_COVERAGE_FILE}"
    return 0
  fi

  log "Loading coverage mapping from ${PTS_COVERAGE_FILE}"

  local first
  first="$(head -c 1 "${PTS_COVERAGE_FILE}" 2>/dev/null || true)"

  if [[ "${first}" == "{" || "${first}" == "[" ]]; then
    # JSON coverage map.
    while IFS=$'\t' read -r pattern target; do
      [[ -n "${pattern}" && -n "${target}" ]] && COVERAGE_MAP["${pattern}"]="${target}"
    done < <(python3 - <<PY
import json, sys
with open("${PTS_COVERAGE_FILE}") as fh:
    data = json.load(fh)
for m in data.get("mappings", []):
    print(f"{m.get('pattern','')}\t{m.get('target','')}")
PY
)
  else
    # TSV coverage map.
    while IFS=$'\t' read -r pattern target; do
      [[ -z "${pattern}" || "${pattern}" == \#* ]] && continue
      [[ -n "${target}" ]] && COVERAGE_MAP["${pattern}"]="${target}"
    done < "${PTS_COVERAGE_FILE}"
  fi

  log "Loaded ${#COVERAGE_MAP[@]} coverage mapping(s)"
}

# Map a single changed file to zero or more test targets using the coverage map.
# Results are appended to the global __PTS_CURRENT_TARGETS array.
map_file_via_coverage() {
  local file="$1"
  for pattern in "${!COVERAGE_MAP[@]}"; do
    # shellcheck disable=SC2053
    if [[ "${file}" == ${pattern} ]]; then
      local target="${COVERAGE_MAP[${pattern}]}"
      __PTS_CURRENT_TARGETS+=("${target}")
      log_debug "  coverage map: ${file} -> ${target}"
    fi
  done
}

# ---------------------------------------------------------------------------
# Heuristic test target discovery
# ---------------------------------------------------------------------------

# Append Go package targets for the changed file to __PTS_CURRENT_TARGETS.
heuristic_go_targets() {
  local file="$1"

  # If a Go test file changed, run its package.
  if [[ "${file}" == *_test.go ]]; then
    local pkg
    pkg="$(go list -f '{{.ImportPath}}' "./$(dirname "${file}")" 2>/dev/null || true)"
    [[ -n "${pkg}" ]] && __PTS_CURRENT_TARGETS+=("${pkg}")
    return 0
  fi

  # If a regular .go file changed, prefer its sibling _test.go package.
  if [[ "${file}" == *.go && -f "${file%.go}_test.go" ]]; then
    local pkg
    pkg="$(go list -f '{{.ImportPath}}' "./$(dirname "${file}")" 2>/dev/null || true)"
    [[ -n "${pkg}" ]] && __PTS_CURRENT_TARGETS+=("${pkg}")
  fi
}

# Append TypeScript/JavaScript test file targets for the changed file.
heuristic_jsts_targets() {
  local file="$1"
  local base dir

  case "${file}" in
    *.ts|*.tsx|*.js|*.jsx)
      base="${file%.*}"
      dir="$(dirname "${file}")"
      local -a candidates=(
        "${base}.test.ts"
        "${base}.test.tsx"
        "${base}.test.js"
        "${base}.test.jsx"
        "${base}.spec.ts"
        "${base}.spec.tsx"
        "${base}.spec.js"
        "${base}.spec.jsx"
        "${dir}/__tests__/$(basename "${base}").test.ts"
        "${dir}/__tests__/$(basename "${base}").test.tsx"
        "${dir}/__tests__/$(basename "${base}").test.js"
        "${dir}/__tests__/$(basename "${base}").test.jsx"
      )
      for cand in "${candidates[@]}"; do
        if [[ -f "${cand}" && "${cand}" != *:Zone.Identifier ]]; then
          __PTS_CURRENT_TARGETS+=("${cand}")
          log_debug "  jsts heuristic: ${file} -> ${cand}"
          break
        fi
      done
      ;;
  esac
}

# Return 0 if the path is a recognized test file, non-zero otherwise.
# Used both by heuristics and by the non-Go target normalizer.
is_test_file_path() {
  local path="$1"
  [[ "${path}" =~ (_test\.go|\.test\.(ts|tsx|js|jsx|mjs|cjs)|\.spec\.(ts|tsx|js|jsx|mjs|cjs)|_test\.(py|rb|rs)|test_\.(py|rb))$ ]]
}

# Append generic test file targets found by name similarity.
# This is a last-resort fallback for languages without dedicated handlers.
heuristic_generic_targets() {
  local file="$1"
  local dir base

  # Skip files already handled by language-specific heuristics.
  case "${file}" in
    *.go|*.ts|*.tsx|*.js|*.jsx) return 0 ;;
  esac

  dir="$(dirname "${file}")"
  base="$(basename "${file%.*}")"

  # Try to find any test files that share the same base name in the same dir.
  while IFS= read -r cand; do
    if [[ "${cand}" == *:Zone.Identifier ]]; then
      continue
    fi
    if ! is_test_file_path "${cand}"; then
      continue
    fi
    __PTS_CURRENT_TARGETS+=("${cand}")
    log_debug "  generic heuristic: ${file} -> ${cand}"
  done < <(find "${dir}" -maxdepth 1 -type f \( \
    -name "${base}.test.*" -o \
    -name "${base}.spec.*" -o \
    -name "${base}_test.*" -o \
    -name "test_${base}.*" \
  \) 2>/dev/null)
}

# Combine coverage map and heuristic discovery for one file.
map_file_heuristically() {
  local file="$1"
  heuristic_go_targets "${file}"
  heuristic_jsts_targets "${file}"
  heuristic_generic_targets "${file}"
}

# ---------------------------------------------------------------------------
# Target normalization and filtering
# ---------------------------------------------------------------------------

# Normalize a raw target into a form usable by the configured test runner.
# For Go runners this means package import paths; for other runners it means
# existing test file paths.  Returns an empty string when the target should be
# dropped.
normalize_target() {
  local raw="$1"

  # Drop Windows alternate data stream artifacts and obvious non-tests.
  if [[ "${raw}" == *:Zone.Identifier || "${raw}" == *.Zone.Identifier ]]; then
    echo ""
    return 0
  fi

  # If the runner is Go, convert file paths to package paths and skip
  # non-Go targets.
  if [[ "${PTS_TEST_RUNNER}" == "go test"* || "${PTS_TEST_RUNNER}" == go\ test* ]]; then
    if [[ "${raw}" == *_test.go ]]; then
      go list -f '{{.ImportPath}}' "./$(dirname "${raw}")" 2>/dev/null || true
    elif [[ "${raw}" == *.go ]]; then
      # Source file with no sibling test file; drop it.
      echo ""
    elif [[ "${raw}" =~ \.[a-zA-Z0-9_]+$ ]]; then
      # Has a file extension but is not a Go test/source file; drop it.
      echo ""
    elif [[ "${raw}" == */* ]]; then
      # Looks like a Go import path (contains a slash, no file extension).
      echo "${raw}"
    elif [[ -d "${raw}" ]]; then
      # Local directory; ask go list for the import path.
      go list -f '{{.ImportPath}}' "./${raw}" 2>/dev/null || true
    else
      # Non-Go file target (e.g. JS test) cannot be run by go test.
      echo ""
    fi
    return 0
  fi

  # For non-Go runners, keep existing file targets that look like tests.
  if [[ -f "${raw}" ]] && is_test_file_path "${raw}"; then
    echo "${raw}"
  else
    echo ""
  fi
}

# Rebuild TEST_TARGETS, keeping only normalized targets usable by the runner.
filter_targets_for_runner() {
  local -A normalized=()
  for t in "${!TEST_TARGETS[@]}"; do
    local trigger="${TEST_TARGETS[${t}]}"
    local nt
    nt="$(normalize_target "${t}")"
    if [[ -n "${nt}" ]]; then
      normalized["${nt}"]="${trigger}"
    fi
  done

  TEST_TARGETS=()
  for t in "${!normalized[@]}"; do
    TEST_TARGETS["${t}"]="${normalized[${t}]}"
  done
}

# ---------------------------------------------------------------------------
# Resolve final test target set
# ---------------------------------------------------------------------------

resolve_test_targets() {
  if [[ ${#CHANGED_FILES[@]} -eq 0 ]]; then
    log "No changed files; no test targets selected"
    return 0
  fi

  log "Resolving test targets..."

  for file in "${CHANGED_FILES[@]}"; do
    __PTS_CURRENT_TARGETS=()

    # Coverage map takes precedence.
    map_file_via_coverage "${file}"

    # Fall back to heuristics when no explicit mapping exists.
    if [[ ${#__PTS_CURRENT_TARGETS[@]} -eq 0 ]]; then
      map_file_heuristically "${file}"
    fi

    for t in "${__PTS_CURRENT_TARGETS[@]}"; do
      TEST_TARGETS["${t}"]="${file}"
    done
  done

  filter_targets_for_runner

  log "Selected ${#TEST_TARGETS[@]} test target(s)"
  for t in "${!TEST_TARGETS[@]}"; do
    log_debug "  target: ${t} (triggered by ${TEST_TARGETS[${t}]})"
  done
}

# ---------------------------------------------------------------------------
# Biome lint on changed files
# ---------------------------------------------------------------------------

run_biome_lint() {
  if ! command -v biome &>/dev/null; then
    warn "biome not installed; skipping lint"
    LINT_STATUS=0
    return 0
  fi

  local -a biome_files=()
  for f in "${CHANGED_FILES[@]}"; do
    case "${f}" in
      *.js|*.jsx|*.ts|*.tsx|*.mjs|*.cjs|*.json|*.css|*.html)
        biome_files+=("${f}")
        ;;
    esac
  done

  if [[ ${#biome_files[@]} -eq 0 ]]; then
    log "No Biome-supported files changed; skipping lint"
    LINT_STATUS=0
    return 0
  fi

  log "Running Biome lint on ${#biome_files[@]} file(s)..."

  local biome_args=()
  if [[ -f "${PTS_BIOME_CONFIG}" ]]; then
    # Biome expects --config-path to point at the directory containing biome.json.
    biome_args+=("--config-path=$(dirname "${PTS_BIOME_CONFIG}")")
  elif [[ -d "${PTS_BIOME_CONFIG}" ]]; then
    biome_args+=("--config-path=${PTS_BIOME_CONFIG}")
  fi

  local lint_output
  LINT_STATUS=0
  lint_output="$(biome lint "${biome_args[@]}" --max-diagnostics=200 "${biome_files[@]}" 2>&1)" || LINT_STATUS=$?

  TEST_LOGS+=("BIOME LINT" "${lint_output}")

  if [[ "${LINT_STATUS}" -ne 0 ]]; then
    error "Biome lint failed with status ${LINT_STATUS}"
  else
    log "Biome lint passed"
  fi
}

# ---------------------------------------------------------------------------
# Biome format check on changed files
# ---------------------------------------------------------------------------

run_biome_format() {
  if ! command -v biome &>/dev/null; then
    warn "biome not installed; skipping format check"
    FORMAT_STATUS=0
    return 0
  fi

  local -a biome_files=()
  for f in "${CHANGED_FILES[@]}"; do
    case "${f}" in
      *.js|*.jsx|*.ts|*.tsx|*.mjs|*.cjs|*.json|*.css|*.html)
        biome_files+=("${f}")
        ;;
    esac
  done

  if [[ ${#biome_files[@]} -eq 0 ]]; then
    log "No Biome-supported files changed; skipping format check"
    FORMAT_STATUS=0
    return 0
  fi

  log "Running Biome format check on ${#biome_files[@]} file(s)..."

  local biome_args=()
  if [[ -f "${PTS_BIOME_CONFIG}" ]]; then
    biome_args+=("--config-path=$(dirname "${PTS_BIOME_CONFIG}")")
  elif [[ -d "${PTS_BIOME_CONFIG}" ]]; then
    biome_args+=("--config-path=${PTS_BIOME_CONFIG}")
  fi

  local format_output
  FORMAT_STATUS=0
  format_output="$(biome format "${biome_args[@]}" --max-diagnostics=200 "${biome_files[@]}" 2>&1)" || FORMAT_STATUS=$?

  TEST_LOGS+=("BIOME FORMAT CHECK" "${format_output}")

  if [[ "${FORMAT_STATUS}" -ne 0 ]]; then
    error "Biome format check failed with status ${FORMAT_STATUS}"
  else
    log "Biome format check passed"
  fi

  return "${FORMAT_STATUS}"
}

# ---------------------------------------------------------------------------
# Test execution with self-correction retries
# ---------------------------------------------------------------------------

# Build a test command from the current TEST_TARGETS associative array.
build_test_command() {
  local runner="${PTS_TEST_RUNNER}"
  local -a targets=("${!TEST_TARGETS[@]}")

  if [[ ${#targets[@]} -eq 0 ]]; then
    echo ""
    return 0
  fi

  # For Go, targets are import paths; pass them directly.
  if [[ "${runner}" == "go test"* || "${runner}" == go\ test* ]]; then
    echo "${runner} ${targets[*]}"
  else
    # Generic runner: pass target file paths as positional arguments.
    echo "${runner} ${targets[*]}"
  fi
}

# Expand test targets by broadening Go packages to their parent packages.
# This is the "self-correction" strategy: if narrowly selected tests fail,
# we retry with a wider net.  Expanded targets are normalized again.
expand_test_targets() {
  local -a new_targets=()

  for t in "${!TEST_TARGETS[@]}"; do
    # Broaden Go package import paths to the immediate parent package path.
    if [[ "${t}" == */* && ! "${t}" == *.* ]]; then
      local parent
      parent="${t%/*}"
      [[ -n "${parent}" ]] && new_targets+=("${parent}")
    fi
  done

  # If we had no targets, fall back to the repository root.
  if [[ ${#TEST_TARGETS[@]} -eq 0 ]]; then
    new_targets+=(".")
  fi

  for nt in "${new_targets[@]}"; do
    TEST_TARGETS["${nt}"]="<expanded>"
  done

  filter_targets_for_runner

  log "Expanded test target set to ${#TEST_TARGETS[@]} target(s)"
}

run_tests() {
  local attempt="$1"
  local cmd
  cmd="$(build_test_command)"

  if [[ -z "${cmd}" ]]; then
    log "No test targets to execute"
    TEST_STDERR=""
    return 0
  fi

  log "Running tests (attempt ${attempt})..."
  log "  ${cmd}"

  local test_output
  local test_status=0
  local stderr_file
  stderr_file="$(mktemp)"

  test_output="$(eval "${cmd}" 2> "${stderr_file}")" || test_status=$?
  TEST_STDERR="$(cat "${stderr_file}" 2>/dev/null || true)"
  rm -f "${stderr_file}"

  TEST_LOGS+=("TEST ATTEMPT ${attempt}" "${cmd}" "${test_output}")
  if [[ -n "${TEST_STDERR}" ]]; then
    TEST_LOGS+=("TEST STDERR ${attempt}" "${TEST_STDERR}")
  fi

  if [[ "${test_status}" -ne 0 ]]; then
    warn "Test attempt ${attempt} failed with status ${test_status}"
  else
    log "Test attempt ${attempt} passed"
  fi

  return "${test_status}"
}

self_correction_loop() {
  if [[ ${#TEST_TARGETS[@]} -eq 0 ]]; then
    log "No tests selected; skipping execution"
    TEST_STDERR=""
    return 0
  fi

  local attempt=1
  while true; do
    if run_tests "${attempt}"; then
      return 0
    fi

    # Feed captured stderr back to the WASM actor over NATS.
    publish_selfcorrect_feedback "${attempt}" "${TEST_STDERR}"

    if [[ "${attempt}" -ge "${MAX_RETRIES}" ]]; then
      error "Exhausted ${MAX_RETRIES} self-correction turn(s); tests still failing"
      return 1
    fi

    log "Entering self-correction: expanding test selection for retry"
    expand_test_targets
    attempt=$((attempt + 1))
  done
}

# ---------------------------------------------------------------------------
# Report generation
# ---------------------------------------------------------------------------

ensure_report_dir() {
  mkdir -p "${PTS_REPORT_DIR}"
}

json_escape() {
  python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().rstrip("\n")), end="")' <<< "$1"
}

write_text_report() {
  local report_file
  report_file="${PTS_REPORT_DIR}/pts-report-$(date +%Y%m%d-%H%M%S).txt"
  {
    echo "Predictive Test Selection Report"
    echo "Generated: $(date -Iseconds)"
    echo "Repository: $(git rev-parse --show-toplevel)"
    echo "HEAD: $(git rev-parse HEAD 2>/dev/null || echo 'unknown')"
    echo ""
    echo "Scope: staged=${STAGED} unstaged=${UNSTAGED}"
    echo "Self-correction context: owner=${SELF_CORRECT_OWNER} session=${SELF_CORRECT_SESSION}"
    echo "Changed files (${#CHANGED_FILES[@]}):"
    for f in "${CHANGED_FILES[@]}"; do echo "  - ${f}"; done
    echo ""
    echo "Structural rules matched (${#MUTATION_RULES[@]}):"
    for r in "${MUTATION_RULES[@]}"; do echo "  - ${r}"; done
    echo ""
    echo "Selected test targets (${#TEST_TARGETS[@]}):"
    for t in "${!TEST_TARGETS[@]}"; do echo "  - ${t} (trigger: ${TEST_TARGETS[${t}]})"; done
    echo ""
    echo "Lint status: ${LINT_STATUS}"
    echo "Format status: ${FORMAT_STATUS}"
    echo ""
    echo "--- Execution logs ---"
    for entry in "${TEST_LOGS[@]}"; do
      echo "${entry}"
    done
  } > "${report_file}"

  log "Text report written to ${report_file}"
}

write_json_output() {
  local overall_status="passed"
  if [[ "${LINT_STATUS}" -ne 0 || "${FORMAT_STATUS}" -ne 0 ]]; then
    overall_status="failed"
  fi

  {
    printf '{\n'
    printf '  "status": %s,\n' "$(json_escape "${overall_status}")"
    printf '  "timestamp": %s,\n' "$(json_escape "$(date -Iseconds)")"
    printf '  "head": %s,\n' "$(json_escape "$(git rev-parse HEAD 2>/dev/null || echo 'unknown')")"
    printf '  "scope": {"staged": %s, "unstaged": %s},\n' "${STAGED}" "${UNSTAGED}"
    printf '  "self_correction": {"owner": %s, "session": %s, "max_retries": %s},\n' \
      "$(json_escape "${SELF_CORRECT_OWNER}")" "$(json_escape "${SELF_CORRECT_SESSION}")" "${MAX_RETRIES}"

    printf '  "changed_files": [\n'
    local first=1
    for f in "${CHANGED_FILES[@]}"; do
      [[ "${first}" -eq 1 ]] || printf ',\n'
      first=0
      printf '    %s' "$(json_escape "${f}")"
    done
    printf '\n  ],\n'

    printf '  "mutation_rules": [\n'
    first=1
    for r in "${MUTATION_RULES[@]}"; do
      [[ "${first}" -eq 1 ]] || printf ',\n'
      first=0
      printf '    %s' "$(json_escape "${r}")"
    done
    printf '\n  ],\n'

    printf '  "test_targets": {\n'
    first=1
    for t in "${!TEST_TARGETS[@]}"; do
      [[ "${first}" -eq 1 ]] || printf ',\n'
      first=0
      printf '    %s: %s' "$(json_escape "${t}")" "$(json_escape "${TEST_TARGETS[${t}]}")"
    done
    printf '\n  },\n'

    printf '  "lint_status": %s,\n' "${LINT_STATUS}"
    printf '  "format_status": %s,\n' "${FORMAT_STATUS}"
    printf '  "max_retries": %s,\n' "${MAX_RETRIES}"

    printf '  "logs": [\n'
    first=1
    for entry in "${TEST_LOGS[@]}"; do
      [[ "${first}" -eq 1 ]] || printf ',\n'
      first=0
      printf '    %s' "$(json_escape "${entry}")"
    done
    printf '\n  ]\n'

    printf '}\n'
  }
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

main() {
  parse_args "$@"
  require_cmds
  ensure_git_root
  init_selfcorrect_context
  ensure_report_dir

  discover_changed_files
  load_coverage_map
  run_ast_grep_scan
  resolve_test_targets
  run_biome_lint
  run_biome_format

  local test_status=0
  if ! self_correction_loop; then
    test_status=1
  fi

  if [[ "${JSON_OUTPUT}" -eq 1 ]]; then
    write_json_output
  else
    write_text_report
  fi

  if [[ "${LINT_STATUS}" -ne 0 || "${FORMAT_STATUS}" -ne 0 || "${test_status}" -ne 0 ]]; then
    error "PTS gate failed"
    return 1
  fi

  log "PTS gate passed"
  return 0
}

main "$@"
