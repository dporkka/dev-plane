#!/usr/bin/env bash
# Synthetic, isolated, localhost-only review staging smoke.
# No production credentials, persistent data, GitHub OAuth, or agent execution.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
API_PORT="${STAGING_API_PORT:-56381}"
WEB_PORT="${STAGING_WEB_PORT:-56382}"
DB_PORT="${STAGING_DB_PORT:-55461}"
SESSION_DIR=""
API_PID=""
WEB_PID=""
PG_STARTED=0

cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  if [[ -n "$WEB_PID" ]]; then
    kill "$WEB_PID" 2>/dev/null || true
    wait "$WEB_PID" 2>/dev/null || true
  fi
  if [[ -n "$API_PID" ]]; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
  fi
  if [[ "$PG_STARTED" -eq 1 && -n "$SESSION_DIR" ]]; then
    pg_ctl -D "$SESSION_DIR/postgres" -m fast stop >/dev/null 2>&1 || true
  fi
  if [[ -n "$SESSION_DIR" && -d "$SESSION_DIR" && "$SESSION_DIR" == /tmp/devplane-review-stage.* ]]; then
    rm -rf -- "$SESSION_DIR"
  fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

fail() { echo "STAGING ERROR: $*" >&2; exit 1; }
for tool in git go node npm curl psql initdb pg_ctl createdb openssl mktemp; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    if [[ "$tool" == "initdb" || "$tool" == "pg_ctl" || "$tool" == "createdb" || "$tool" == "psql" ]]; then
      pg_bin="$(pg_config --bindir 2>/dev/null || true)"
      if [[ -n "$pg_bin" ]]; then export PATH="$pg_bin:$PATH"; fi
    fi
    command -v "$tool" >/dev/null 2>&1 || fail "missing required tool: $tool"
  fi
done
for item in "$API_PORT" "$WEB_PORT" "$DB_PORT"; do
  [[ "$item" =~ ^[0-9]{4,5}$ ]] && ((10#$item > 1024 && 10#$item < 65536)) ||
    fail "ports must be numeric and in range 1025–65535"
done
[[ "$API_PORT" != "$WEB_PORT" && "$API_PORT" != "$DB_PORT" && "$WEB_PORT" != "$DB_PORT" ]] ||
  fail "staging ports must be distinct"

# Running the real services from the checkout can inadvertently read project
# .env files. Fail rather than use potentially production-scoped credentials.
for f in "$ROOT/.env" "$ROOT/apps/api/.env" "$ROOT/apps/web/.env"          "$ROOT/apps/web/.env.local" "$ROOT/apps/web/.env.production"          "$ROOT/apps/web/.env.production.local"; do
  [[ ! -e "$f" ]] || fail "local environment file is present; staging refuses to load it: $f"
done

[[ "$(id -u)" -ne 0 ]] || fail "run as a non-root user"
SESSION_DIR="$(mktemp -d /tmp/devplane-review-stage.XXXXXXXX)"
chmod 700 "$SESSION_DIR"
mkdir -p "$SESSION_DIR/apps/api" "$SESSION_DIR/packages/db" "$SESSION_DIR/home"
cp -a "$ROOT/packages/db/migrations" "$SESSION_DIR/packages/db/migrations"

SHA="$(git -C "$ROOT" rev-parse HEAD)"
echo "Review staging source SHA: $SHA"
if [[ -n "$(git -C "$ROOT" status --porcelain)" ]]; then
  echo "WARNING: source worktree has uncommitted changes; this is NOT a release qualification."
fi

API_ORIGIN="http://127.0.0.1:$API_PORT"
WEB_ORIGIN="http://127.0.0.1:$WEB_PORT"
DB_URL="postgres://$(id -un)@127.0.0.1:$DB_PORT/devplane_review_stage_qa?sslmode=disable"
JWT_SECRET="$(openssl rand -hex 32)"
[[ "${#JWT_SECRET}" -ge 32 ]] || fail "failed to generate synthetic JWT secret"

echo "Initializing disposable PostgreSQL (loopback only)"
initdb -D "$SESSION_DIR/postgres" --auth-local=trust --auth-host=trust --no-instructions > "$SESSION_DIR/postgres-init.log" 2>&1 ||
  fail "PostgreSQL init failed"
pg_ctl -D "$SESSION_DIR/postgres" -l "$SESSION_DIR/postgres.log"   -o "-h 127.0.0.1 -p $DB_PORT -k $SESSION_DIR" start > "$SESSION_DIR/postgres-start.log" 2>&1 ||
  fail "PostgreSQL startup failed; port $DB_PORT may be occupied"
PG_STARTED=1
createdb -h 127.0.0.1 -p "$DB_PORT" devplane_review_stage_qa ||
  fail "could not create disposable staging QA database"

echo "Building API + SDK + production web app"
# Isolate Go compiler telemetry from this machine's shared/corrupted counter
# files; avoid concurrent compiler processes when staging on a busy host.
mkdir -p "$SESSION_DIR/go-config"
(cd "$ROOT/apps/api" && XDG_CONFIG_HOME="$SESSION_DIR/go-config" GOTELEMETRY=off go build -p 1 -o "$SESSION_DIR/api" ./cmd/api)
(cd "$ROOT/packages/sdk/typescript" && npm ci --ignore-scripts --no-audit --no-fund >/dev/null && npm run build >/dev/null)
(cd "$ROOT/apps/web" && npm ci --ignore-scripts --no-audit --no-fund >/dev/null)
SDK_INSTALLED_DIST="$ROOT/apps/web/node_modules/@ai-cp/dev-plane-sdk/dist"
mkdir -p "$SDK_INSTALLED_DIST"
cp -a "$ROOT/packages/sdk/typescript/dist/." "$SDK_INSTALLED_DIST/"
(cd "$ROOT/apps/web" && env -i HOME="$SESSION_DIR/home" PATH="$PATH"   NEXT_PUBLIC_API_URL="$API_ORIGIN" NEXT_TELEMETRY_DISABLED=1   npm run build > "$SESSION_DIR/web-build.log" 2>&1) || {
    tail -40 "$SESSION_DIR/web-build.log" >&2
    fail "web build failed"
  }
(cd "$ROOT/scripts/staging" && PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm ci --ignore-scripts --no-audit --no-fund >/dev/null)

echo "Starting isolated API and applying migrations"
(
  cd "$SESSION_DIR/apps/api"
  exec env -i PATH="$PATH" HOME="$SESSION_DIR/home"     JWT_SECRET="$JWT_SECRET" DATABASE_URL="$DB_URL"     ALLOWED_ORIGINS="$WEB_ORIGIN" PORT="$API_PORT" API_BIND_HOST=127.0.0.1     OAUTH_COOKIE_SECURE=false WORKSPACE_BASE_DIR="$SESSION_DIR/workspaces"     WORKSPACE_RUNTIME=local ENABLE_TEMPORAL=false ENABLE_SANDBOXED_RUNTIME=false     NATS_URL=nats://127.0.0.1:55469 LOG_LEVEL=warn     "$SESSION_DIR/api"
) > "$SESSION_DIR/api.log" 2>&1 &
API_PID=$!

wait_http() {
  local label="$1" url="$2" pid="$3" log="$4"
  for _ in $(seq 1 80); do
    if curl --silent --show-error --fail --max-time 1 "$url" >/dev/null 2>&1; then return 0; fi
    if ! kill -0 "$pid" 2>/dev/null; then
      tail -35 "$log" >&2
      fail "$label terminated before becoming ready"
    fi
    sleep 0.5
  done
  tail -35 "$log" >&2
  fail "$label did not become ready"
}
wait_http "API" "$API_ORIGIN/ready" "$API_PID" "$SESSION_DIR/api.log"
psql -X -v ON_ERROR_STOP=1 "$DB_URL" -f "$ROOT/scripts/staging/seed-review-fixtures.sql" > "$SESSION_DIR/seed.log"

echo "Starting production web app on loopback"
(
  cd "$ROOT/apps/web"
  exec env -i PATH="$PATH" HOME="$SESSION_DIR/home"     NEXT_PUBLIC_API_URL="$API_ORIGIN" NEXT_TELEMETRY_DISABLED=1 NODE_ENV=production     npm run start -- --hostname 127.0.0.1 --port "$WEB_PORT"
) > "$SESSION_DIR/web.log" 2>&1 &
WEB_PID=$!
wait_http "web" "$WEB_ORIGIN/reviews" "$WEB_PID" "$SESSION_DIR/web.log"

echo "Running genuine JWT / PostgreSQL / browser smoke (no mocks)"
# Clean HOME prevents loading user profiles; only browser binaries are shared.
BROWSER_CACHE="${PLAYWRIGHT_BROWSERS_PATH:-$HOME/.cache/ms-playwright}"
(
  cd "$ROOT/scripts/staging"
  env -i PATH="$PATH" HOME="$SESSION_DIR/home" PLAYWRIGHT_BROWSERS_PATH="$BROWSER_CACHE" STAGING_TEST_JWT_SECRET="$JWT_SECRET" STAGING_API_URL="$API_ORIGIN" STAGING_WEB_URL="$WEB_ORIGIN" node review-smoke.cjs
)

echo "PASS: isolated local staging review smoke for $SHA"
echo "Reminder: GitHub OAuth, remote staging, and independent approval are NOT verified by this test."
