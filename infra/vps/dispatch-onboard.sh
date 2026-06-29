#!/usr/bin/env bash
# dispatch-onboard.sh -- Idempotent VPS onboarding for the Multi-Agent Developer Mesh.
#
# Responsibilities:
#   - Detect OS family (Debian/Ubuntu/Fedora) and install runtime dependencies.
#   - Discover physical CPU cores and total RAM.
#   - Compute tmpfs=40% RAM, NATS JetStream memory store=5% RAM, cgroups MemoryMax=80% RAM.
#   - Configure zswap with lz4 compressor.
#   - Mount a tmpfs RAM disk at /mnt/agent-swarms.
#   - Install and configure PgBouncer for transaction pooling.
#   - Generate systemd slice drop-ins for cgroups v2 resource control.
#
# Usage:
#   ./dispatch-onboard.sh [--pgbouncer-db DB] [--pgbouncer-user USER]
#
# The script is idempotent: running it twice will not duplicate mounts or configs.

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults and constants
# ---------------------------------------------------------------------------
PGBOUNCER_DB="${PGBOUNCER_DB:-agentmesh}"
PGBOUNCER_USER="${PGBOUNCER_USER:-agent_app}"
PGBOUNCER_AUTH_USER="${PGBOUNCER_AUTH_USER:-pgbouncer_auth}"
AGENT_TMPFS="/mnt/agent-swarms"
SYSTEMD_SLICE_DIR="/etc/systemd/system/agent-mesh-workers.slice.d"
NATS_CONF_DIR="/etc/nats"

# ---------------------------------------------------------------------------
# Logging helpers
# ---------------------------------------------------------------------------
log() { printf '[%s] %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"; }
info() { log "INFO: $*"; }
warn() { log "WARN: $*" >&2; }
fatal() { log "FATAL: $*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# OS detection
# ---------------------------------------------------------------------------
detect_os_family() {
  if [[ -f /etc/os-release ]]; then
    # shellcheck source=/dev/null
    source /etc/os-release
    case "$ID" in
      debian|ubuntu) echo "debian" ;;
      fedora|rhel|centos|rocky|almalinux) echo "fedora" ;;
      *)
        case "$ID_LIKE" in
          *debian*|*ubuntu*) echo "debian" ;;
          *fedora*|*rhel*|*centos*) echo "fedora" ;;
          *) fatal "Unsupported OS: ID=$ID ID_LIKE=$ID_LIKE" ;;
        esac
        ;;
    esac
  else
    fatal "/etc/os-release missing; cannot detect OS family"
  fi
}

OS_FAMILY=$(detect_os_family)
info "Detected OS family: $OS_FAMILY"

# ---------------------------------------------------------------------------
# Hardware discovery
# ---------------------------------------------------------------------------
physical_cores() {
  # Prefer lscpu because /proc/cpuinfo is hyperthread-ambiguous on some VMs.
  if command -v lscpu >/dev/null 2>&1; then
    lscpu -p | awk -F',' '/^[^#]/{print $1}' | sort -u | wc -l
  else
    grep -c '^processor' /proc/cpuinfo
  fi
}

total_ram_bytes() {
  awk '/MemTotal/ {print $2 * 1024}' /proc/meminfo
}

CORES=$(physical_cores)
RAM_BYTES=$(total_ram_bytes)
info "Physical CPU cores: $CORES"
info "Total RAM: $RAM_BYTES bytes"

# ---------------------------------------------------------------------------
# Resource formulas (no hardcoded values)
# ---------------------------------------------------------------------------
compute_bytes() {
  local percent="$1"
  awk -v ram="$RAM_BYTES" -v pct="$percent" 'BEGIN {printf "%d", ram * pct / 100}'
}

TMPFS_BYTES=$(compute_bytes 40)
NATS_MEMSTORE_BYTES=$(compute_bytes 5)
CGROUP_MEMORY_MAX=$(compute_bytes 80)

# Round tmpfs and NATS values to whole MiB for readability.
round_mib() { awk -v b="$1" 'BEGIN {printf "%d", int((b + 1024*1024 - 1) / (1024*1024))}'; }
TMPFS_MIB=$(round_mib "$TMPFS_BYTES")
NATS_MEMSTORE_MIB=$(round_mib "$NATS_MEMSTORE_BYTES")

info "Computed tmpfs size: ${TMPFS_MIB}MiB (40% RAM)"
info "Computed NATS JetStream memory store: ${NATS_MEMSTORE_MIB}MiB (5% RAM)"
info "Computed cgroups MemoryMax: ${CGROUP_MEMORY_MAX} bytes (80% RAM)"

# ---------------------------------------------------------------------------
# Dependency installation
# ---------------------------------------------------------------------------
ensure_deps() {
  info "Installing dependencies for $OS_FAMILY"
  case "$OS_FAMILY" in
    debian)
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -q
      apt-get install -y -q \
        curl wget ca-certificates gnupg2 \
        pgbouncer postgresql-client \
        systemd systemd-sysv \
        util-linux coreutils awk jq
      ;;
    fedora)
      dnf -y update
      dnf -y install \
        curl wget ca-certificates \
        pgbouncer postgresql \
        systemd systemd-sysv \
        util-linux coreutils awk jq
      ;;
    *) fatal "Unhandled OS family: $OS_FAMILY" ;;
  esac
}

# ---------------------------------------------------------------------------
# zswap configuration
# ---------------------------------------------------------------------------
ensure_zswap() {
  info "Configuring zswap (lz4)"
  local zdir="/sys/module/zswap/parameters"
  if [[ -d "$zdir" ]]; then
    echo lz4 > "$zdir/compressor" || warn "Could not set zswap compressor"
    echo 1 > "$zdir/enabled"      || warn "Could not enable zswap"
    # 20% of RAM is a sane max pool percentage for swap-heavy agent workloads.
    echo 20 > "$zdir/max_pool_percent" || warn "Could not set zswap pool percent"
  else
    warn "zswap kernel module not available"
  fi
}

# ---------------------------------------------------------------------------
# tmpfs RAM disk
# ---------------------------------------------------------------------------
ensure_tmpfs() {
  info "Ensuring tmpfs RAM disk at $AGENT_TMPFS"
  if ! mountpoint -q "$AGENT_TMPFS"; then
    mkdir -p "$AGENT_TMPFS"
    # nodev,noexec,nosuid are deliberately omitted from the agent swarm fs so
    # WASM executables can be launched directly.  If your threat model requires
    # stricter mounts, add noexec and place executables on a separate volume.
    mount -t tmpfs -o "size=${TMPFS_MIB}m,mode=0755,uid=0,gid=0" agent-swarms "$AGENT_TMPFS"
    info "Mounted $AGENT_TMPFS"
  else
    info "$AGENT_TMPFS already mounted; skipping"
  fi

  # Persist across reboots.
  local fstab_line="agent-swarfs $AGENT_TMPFS tmpfs size=${TMPFS_MIB}m,mode=0755 0 0"
  if ! grep -qF "$fstab_line" /etc/fstab; then
    echo "$fstab_line" >> /etc/fstab
    info "Added $AGENT_TMPFS to /etc/fstab"
  fi
}

# ---------------------------------------------------------------------------
# PgBouncer configuration
# ---------------------------------------------------------------------------
generate_pgbouncer_ini() {
  local out="/etc/pgbouncer/pgbouncer.ini"
  mkdir -p "$(dirname "$out")"

  # Generate a random password if the auth user password is not provided.
  local auth_password
  auth_password="${PGBOUNCER_AUTH_PASSWORD:-$(openssl rand -hex 32)}"

  cat > "$out" <<EOF
; Generated by dispatch-onboard.sh on $(date -u '+%Y-%m-%dT%H:%M:%SZ')
[databases]
${PGBOUNCER_DB} = host=127.0.0.1 port=5432 dbname=${PGBOUNCER_DB}

[pgbouncer]
listen_port = 6432
listen_addr = 127.0.0.1
auth_type = hba
auth_file = /etc/pgbouncer/userlist.txt
auth_hba_file = /etc/pgbouncer/pg_hba.conf
admin_users = ${PGBOUNCER_AUTH_USER}
stats_users = ${PGBOUNCER_AUTH_USER}

; Transaction pooling is ideal for short agent-service connections.
pool_mode = transaction
max_client_conn = 10000
max_db_connections = 200
default_pool_size = 25
min_pool_size = 5
reserve_pool_size = 10
reserve_pool_timeout = 3
server_idle_timeout = 600
server_lifetime = 3600
server_connect_timeout = 15
server_login_retry = 15
query_timeout = 0
query_wait_timeout = 120
client_idle_timeout = 0
client_login_timeout = 60
idle_transaction_timeout = 0

; Logging
logfile = /var/log/pgbouncer/pgbouncer.log
pidfile = /run/pgbouncer/pgbouncer.pid

; TLS (backends commonly terminate TLS on localhost; enable if certs provided)
; server_tls_sslmode = require
; server_tls_ca_file = /etc/pgbouncer/ca.crt
EOF

  # userlist.txt with MD5-hashed credentials.
  local md5pw
  md5pw=$(echo -n "${auth_password}${PGBOUNCER_AUTH_USER}" | md5sum | awk '{print $1}')
  cat > /etc/pgbouncer/userlist.txt <<EOF
"${PGBOUNCER_AUTH_USER}" "md5${md5pw}"
"${PGBOUNCER_USER}" "${PGBOUNCER_USER_PASSWORD:-placeholder_change_me}"
EOF
  chmod 640 /etc/pgbouncer/userlist.txt

  # Minimal pg_hba.conf for local access.
  cat > /etc/pgbouncer/pg_hba.conf <<EOF
local  all  all  trust
host   all  all  127.0.0.1/32  scram-sha-256
host   all  all  ::1/128       scram-sha-256
EOF

  info "Generated $out"
  info "NOTE: Set PGBOUNCER_USER_PASSWORD before starting PgBouncer."
}

ensure_pgbouncer() {
  info "Configuring PgBouncer"
  generate_pgbouncer_ini
  mkdir -p /var/log/pgbouncer /run/pgbouncer
  chown -R postgres:postgres /var/log/pgbouncer /run/pgbouncer /etc/pgbouncer 2>/dev/null || true

  case "$OS_FAMILY" in
    debian)
      systemctl enable pgbouncer || true
      systemctl restart pgbouncer || warn "PgBouncer restart failed"
      ;;
    fedora)
      systemctl enable pgbouncer || true
      systemctl restart pgbouncer || warn "PgBouncer restart failed"
      ;;
  esac
}

# ---------------------------------------------------------------------------
# cgroups v2 systemd slice drop-ins
# ---------------------------------------------------------------------------
ensure_cgroups() {
  info "Generating cgroups v2 slice drop-ins"
  mkdir -p "$SYSTEMD_SLICE_DIR"

  cat > "$SYSTEMD_SLICE_DIR/10-resources.conf" <<EOF
# Generated by dispatch-onboard.sh on $(date -u '+%Y-%m-%dT%H:%M:%SZ')
[Slice]
# Worker processes together may consume up to 80% of total RAM.
MemoryMax=${CGROUP_MEMORY_MAX}
MemorySwapMax=0
# Allow burst up to the full worker budget.
MemoryHigh=${CGROUP_MEMORY_MAX}

# CPU: reserve one core for system/mesh overhead; workers share the rest.
CPUQuotaPerSecUSec=$(( (CORES - 1) * 100 ))%

# PIDs: generous ceiling for 1,000+ agents.
TasksMax=200000

# OOM behavior: prefer killing the worker cgroup over the entire node.
MemoryOOMPolicy=kill
EOF

  # Per-agent sub-slice template.  Operators can instantiate these with
  # systemctl start agent-mesh-worker@<id>.slice.
  mkdir -p /etc/systemd/system
  cat > /etc/systemd/system/agent-mesh-worker@.slice <<'EOF'
# Template slice for a single agent worker.  Instantiated by the orchestrator.
[Slice]
# Inherit limits from the parent agent-mesh-workers.slice.
MemoryMax=10%
MemorySwapMax=0
CPUWeight=100
IOWeight=100
TasksMax=4096
EOF

  systemctl daemon-reload || warn "systemctl daemon-reload failed"
  info "cgroups v2 slice configuration installed"
}

# ---------------------------------------------------------------------------
# NATS JetStream memory-store helper
# ---------------------------------------------------------------------------
ensure_nats_stub() {
  info "Writing NATS memory-store hint to $NATS_CONF_DIR"
  mkdir -p "$NATS_CONF_DIR"
  cat > "$NATS_CONF_DIR/.memory-store" <<EOF
NATS_JETSTREAM_MEMORY_STORE_BYTES=${NATS_MEMSTORE_BYTES}
NATS_JETSTREAM_MEMORY_STORE_MIB=${NATS_MEMSTORE_MIB}
EOF
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
  info "Starting dispatch-onboard.sh"

  # Allow override of DB/user via environment or CLI.
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --pgbouncer-db)    PGBOUNCER_DB="$2"; shift 2 ;;
      --pgbouncer-user)  PGBOUNCER_USER="$2"; shift 2 ;;
      *) warn "Unknown argument: $1"; shift ;;
    esac
  done

  ensure_deps
  ensure_zswap
  ensure_tmpfs
  ensure_pgbouncer
  ensure_cgroups
  ensure_nats_stub

  info "Onboarding complete."
  info "Summary: cores=$CORES ram=${RAM_BYTES} tmpfs=${TMPFS_MIB}MiB nats_memstore=${NATS_MEMSTORE_MIB}MiB cgroup_memmax=${CGROUP_MEMORY_MAX}"
}

main "$@"
