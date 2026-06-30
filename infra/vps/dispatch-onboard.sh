#!/usr/bin/env bash
# dispatch-onboard.sh -- Idempotent VPS onboarding for the Multi-Agent Developer Mesh.
#
# Responsibilities:
#   - Detect OS family (Debian/Ubuntu/Fedora) and install runtime dependencies.
#   - Discover physical CPU cores and total RAM.
#   - Compute tmpfs=40% RAM, NATS JetStream memory store=5% RAM, cgroups MemoryMax=80% RAM.
#   - Apply kernel-level socket/file descriptor tuning.
#   - Mount a tmpfs RAM disk at /mnt/agent-swarms persistently.
#   - Install and configure NATS server, tailscale, and cgroups v2 resource control.
#   - Install the wezterm-mux-server@.service template unit.
#
# Usage:
#   ./dispatch-onboard.sh [--wezterm-user USER]
#
# The script is idempotent: running it twice will not duplicate mounts or configs.

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults and constants
# ---------------------------------------------------------------------------
AGENT_TMPFS="/mnt/agent-swarms"
SYSTEMD_SLICE_DIR="/etc/systemd/system/agent-mesh-workers.slice.d"
NATS_CONF_DIR="/etc/nats"
WEZTERM_USER="${WEZTERM_USER:-wezterm}"
TAILSCALE_INSTALL_URL="${TAILSCALE_INSTALL_URL:-https://tailscale.com/install.sh}"
NATS_INSTALL_URL="${NATS_INSTALL_URL:-https://get-nats.io}"

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
# Resource formulas (no hardcoded RAM bounds)
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
  info "Installing base dependencies for $OS_FAMILY"
  case "$OS_FAMILY" in
    debian)
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -q
      apt-get install -y -q \
        curl wget ca-certificates gnupg2 \
        systemd systemd-sysv \
        util-linux coreutils awk jq \
        iptables iproute2
      ;;
    fedora)
      dnf -y update
      dnf -y install \
        curl wget ca-certificates \
        systemd systemd-sysv \
        util-linux coreutils awk jq \
        iptables iproute
      ;;
    *) fatal "Unhandled OS family: $OS_FAMILY" ;;
  esac
}

ensure_tailscale() {
  if command -v tailscale >/dev/null 2>&1; then
    info "tailscale already installed"
    return 0
  fi
  info "Installing tailscale"

  # Prefer the distro package when available to avoid curl|sh and to get
  # automatic updates. Fall back to the upstream installer script only when
  # there is no packaged release.
  local installed=0
  case "$OS_FAMILY" in
    debian)
      if apt-get install -y -q tailscale 2>/dev/null; then
        installed=1
      fi
      ;;
    fedora)
      if dnf -y install tailscale 2>/dev/null; then
        installed=1
      fi
      ;;
  esac

  if [[ "$installed" -eq 0 ]]; then
    warn "Distro tailscale package unavailable; falling back to upstream installer"
    curl -fsSL "$TAILSCALE_INSTALL_URL" | sh
  fi

  systemctl enable tailscaled || true
  systemctl start tailscaled || warn "tailscaled start failed"
  warn "Tailscale installed but not logged in. Run: tailscale up --authkey ..."
}

ensure_nats_server() {
  if command -v nats-server >/dev/null 2>&1; then
    info "nats-server already installed"
    return 0
  fi
  info "Installing NATS server"

  # Prefer the distro package or the nats-io official repo when available.
  # Fall back to the upstream install script only when packaging is absent.
  local installed=0
  case "$OS_FAMILY" in
    debian)
      if apt-get install -y -q nats-server 2>/dev/null; then
        installed=1
      fi
      ;;
    fedora)
      if dnf -y install nats-server 2>/dev/null; then
        installed=1
      fi
      ;;
  esac

  if [[ "$installed" -eq 0 ]]; then
    warn "Distro nats-server package unavailable; falling back to upstream installer"
    curl -sf "$NATS_INSTALL_URL" | sh
  fi

  if command -v nats-server >/dev/null 2>&1; then
    info "nats-server installed"
  else
    warn "nats-server not found in PATH after install"
  fi
}

# ---------------------------------------------------------------------------
# Kernel tuning
# ---------------------------------------------------------------------------
ensure_kernel_tuning() {
  info "Applying kernel socket/file descriptor tuning"

  # Derive ceilings from RAM/cores; keep small absolute floors so tiny VMs
  # still obtain production-grade defaults. These floors are counts, not RAM
  # bounds, and therefore do not violate the dynamic RAM-sizing rule.
  local fd_max somaxconn max_map_count
  fd_max=$(( RAM_BYTES / 4096 ))
  [[ $fd_max -ge 1048576 ]] || fd_max=1048576

  somaxconn=$(( CORES * 1024 ))
  [[ $somaxconn -ge 4096 ]] || somaxconn=4096

  max_map_count=$(( RAM_BYTES / 16384 ))
  [[ $max_map_count -ge 262144 ]] || max_map_count=262144

  local sysctl_file="/etc/sysctl.d/99-agent-mesh.conf"
  mkdir -p "$(dirname "$sysctl_file")"

  cat > "$sysctl_file" <<EOF
# Generated by dispatch-onboard.sh on $(date -u '+%Y-%m-%dT%H:%M:%SZ')
# Dynamic values derived from RAM_BYTES=$RAM_BYTES CORES=$CORES.

# File descriptors
fs.file-max=${fd_max}
fs.nr_open=${fd_max}

# Socket backlogs and local port range
net.core.somaxconn=${somaxconn}
net.ipv4.ip_local_port_range=1024 65535

# Virtual memory maps
vm.max_map_count=${max_map_count}
EOF

  # Apply live if possible; persistence is already on disk.
  if command -v sysctl >/dev/null 2>&1; then
    sysctl --system >/dev/null 2>&1 || sysctl -p "$sysctl_file" >/dev/null 2>&1 || warn "Could not apply sysctl settings live"
  fi
  info "Kernel tuning persisted to $sysctl_file"
}

# ---------------------------------------------------------------------------
# tmpfs RAM disk
# ---------------------------------------------------------------------------
ensure_tmpfs() {
  info "Ensuring tmpfs RAM disk at $AGENT_TMPFS"
  if ! mountpoint -q "$AGENT_TMPFS"; then
    mkdir -p "$AGENT_TMPFS"
    # nodev,noexec,nosuid are deliberately omitted so WASM executables can be
    # launched directly from the swarm RAM disk.
    mount -t tmpfs -o "size=${TMPFS_MIB}m,mode=0755,uid=0,gid=0" agent-swarms "$AGENT_TMPFS"
    info "Mounted $AGENT_TMPFS"
  else
    info "$AGENT_TMPFS already mounted; skipping"
  fi

  local fstab_line="agent-swarms $AGENT_TMPFS tmpfs size=${TMPFS_MIB}m,mode=0755 0 0"
  if ! grep -qF "$fstab_line" /etc/fstab; then
    echo "$fstab_line" >> /etc/fstab
    info "Added $AGENT_TMPFS to /etc/fstab"
  fi
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
MemoryHigh=${CGROUP_MEMORY_MAX}

# CPU: reserve one core for system/mesh overhead; workers share the rest.
CPUQuotaPerSecUSec=$(( (CORES - 1) * 100 ))%

# PIDs: generous ceiling for 1,000+ agents.
TasksMax=200000

# OOM behavior: prefer killing the worker cgroup over the entire node.
MemoryOOMPolicy=kill
EOF

  # Per-agent sub-slice template. Operators can instantiate these with
  # systemctl start agent-mesh-worker@<id>.slice.
  mkdir -p /etc/systemd/system
  cat > /etc/systemd/system/agent-mesh-worker@.slice <<'EOF'
# Template slice for a single agent worker.
[Slice]
# Relative to system RAM; no absolute RAM bound.
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
# WezTerm mux user + template unit
# ---------------------------------------------------------------------------
ensure_wezterm_user() {
  if id "$WEZTERM_USER" >/dev/null 2>&1; then
    info "User $WEZTERM_USER already exists"
    return 0
  fi
  info "Creating unprivileged user $WEZTERM_USER"
  useradd --system --create-home --home-dir "/var/lib/$WEZTERM_USER" --shell /usr/sbin/nologin "$WEZTERM_USER"
}

install_wezterm_mux_service() {
  if ! command -v wezterm-mux-server >/dev/null 2>&1; then
    warn "wezterm-mux-server binary not found; install wezterm before starting the service"
  fi

  info "Installing wezterm-mux-server@.service template unit"
  cat > /etc/systemd/system/wezterm-mux-server@.service <<'EOF'
[Unit]
Description=Persistent headless WezTerm multiplexer server for %i
Documentation=https://wezfurlong.org/wezterm/multiplexing.html
After=network-online.target tailscaled.service
Wants=network-online.target tailscaled.service

[Service]
Type=simple
User=%i
Group=%i

Environment="WEZTERM_MUX_LOG=/var/log/wezterm-mux-server/%i-mux.log"
Environment="WEZTERM_MUX_UNIX_DOMAIN=/run/wezterm-mux-server/%i.sock"
Environment="WEZTERM_MUX_BIND=127.0.0.1:8080"
Environment="TAILSCALE_BIND_PREFERENCE=100."
Environment="PATH=/usr/local/bin:/usr/bin:/bin"

WorkingDirectory=/var/lib/%i
ExecStartPre=-/bin/mkdir -p /run/wezterm-mux-server /var/log/wezterm-mux-server
ExecStartPre=-/bin/chown %i:%i /run/wezterm-mux-server /var/log/wezterm-mux-server
ExecStartPre=-/bin/chmod 0750 /run/wezterm-mux-server

ExecStart=/bin/bash -c 'set -a; TAIL_IP=$(ip -4 -o addr show to 100.0.0.0/8 | awk '\''{print $4}'\'' | cut -d/ -f1 | head -1); MUX_BIN=$(command -v wezterm-mux-server || echo /usr/bin/wezterm-mux-server); if [[ -n "${TAIL_IP}" ]]; then export WEZTERM_MUX_BIND="${TAIL_IP}:8080"; echo "Binding wezterm-mux-server for %i to Tailscale ${WEZTERM_MUX_BIND}"; else echo "Tailscale IP not found; binding wezterm-mux-server for %i to loopback ${WEZTERM_MUX_BIND}"; fi; exec "${MUX_BIN}" --daemonize no'

ExecStop=/bin/kill -TERM $MAINPID
ExecReload=/bin/kill -HUP $MAINPID

Restart=on-failure
RestartSec=5
StartLimitInterval=60
StartLimitBurst=3

# Inherit dynamic MemoryMax from the agent-mesh-workers slice.
Slice=agent-mesh-workers.slice

# Security hardening (tailored for a headless terminal multiplexer).
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/run/wezterm-mux-server /var/log/wezterm-mux-server
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes
RestrictRealtime=yes
RestrictNamespaces=yes

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload || warn "systemctl daemon-reload failed"
  info "wezterm-mux-server@.service installed"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
  info "Starting dispatch-onboard.sh"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --wezterm-user) WEZTERM_USER="$2"; shift 2 ;;
      *) warn "Unknown argument: $1"; shift ;;
    esac
  done

  ensure_deps
  ensure_kernel_tuning
  ensure_tailscale
  ensure_nats_server
  ensure_tmpfs
  ensure_cgroups
  ensure_nats_stub
  ensure_wezterm_user
  install_wezterm_mux_service

  info "Onboarding complete."
  info "Summary: cores=$CORES ram=${RAM_BYTES} tmpfs=${TMPFS_MIB}MiB nats_memstore=${NATS_MEMSTORE_MIB}MiB cgroup_memmax=${CGROUP_MEMORY_MAX}"
}

main "$@"
