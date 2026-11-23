#!/usr/bin/env bash
# =============================================================================
# NeoDesk — one-line installer (Linux)
#
# Docker (default: the published image in one container, neodesk-docker.sh):
#   curl -fsSL https://raw.githubusercontent.com/neodeskapp/neodesk/main/install.sh | sudo bash
#
# Docker, move a two-container (split) install to the one container:
#   curl -fsSL .../install.sh | sudo bash -s -- --migrate-split
#
# Native (git clone + neodesk.sh --auto): the newest stable release, or
# the development build while the repository has none:
#   curl -fsSL https://raw.githubusercontent.com/neodeskapp/neodesk/main/install.sh | sudo bash -s -- --native
# Native, the development build: add --channel dev
#
# Options (pass after "bash -s --"):
#   --docker | --native          Installation mode (default: docker)
#   --migrate-split              Replace a split install (server + console containers)
#                                with the one container, which takes its data over
#   --install-dir PATH             Install directory (default: /opt/neodesk)
#   --version TAG                  Docker image tag to pin (default: latest, the newest release)
#   --branch BRANCH                Git branch of the installer files (default: main)
#   --channel stable|dev           Native: release channel (default: stable)
#   --relay-mode auto|local|public Relay auto-detection strategy
#   --relay-servers IP[:port]      Fixed relay address (overrides --relay-mode)
#   --admin-password PASS          Set admin password (Docker: ADMIN_PASSWORD env)
#   --domain NAME                  Docker: HTTPS for the console at this DNS name with a
#                                  Let's Encrypt certificate that Traefik renews by itself
#                                  (the name must point at this server; ports 80 and 443)
#   --letsencrypt-email ADDRESS    Docker: Let's Encrypt's contact (needed with --domain)
#   --letsencrypt-staging          Docker: certificates from Let's Encrypt's staging server
#   --skip-docker-install          Do not install Docker when missing
#   --skip-firewall                Skip UFW/firewalld/iptables configuration
#   --rescue                       Safe repair: permissions, restart, health checks
#   --diagnose                     Read-only diagnostics for Docker deployments
#   --repair-permissions           Only repair Docker data/volume permissions
#   --check-permissions            Check update/config/restart capabilities
#   --set-config KEY=VALUE         Apply an allowlisted runtime setting
#   --purge                        With --uninstall: also remove Docker volumes
#   --uninstall                    Remove NeoDesk Docker installation
#   --help                         Show usage
#
# Environment overrides (same names as flags where applicable):
#   NEODESK_REPO, NEODESK_BRANCH, NEODESK_VERSION, INSTALL_DIR,
#   RELAY_MODE, RELAY_SERVERS, ADMIN_PASSWORD, NEODESK_RAW_BASE,
#   NEODESK_DOMAIN, LETSENCRYPT_EMAIL
# =============================================================================

set -euo pipefail

VERSION="1.0.0"
NEODESK_REPO="${NEODESK_REPO:-neodeskapp/neodesk}"
# The branch whose files this installer fetches (neodesk.sh for a native
# install, neodesk-docker.sh for Docker): the repository's branch, main.
# Override with --branch / NEODESK_BRANCH. The branch names no release
# channel; that is --channel.
NEODESK_INSTALLER_CHANNEL="main"
NEODESK_BRANCH="${NEODESK_BRANCH:-$NEODESK_INSTALLER_CHANNEL}"
# The release channel of a native install: stable or dev; empty leaves it
# to neodesk.sh (stable, or dev while there is no stable release)
RELEASE_CHANNEL=""
# An image tag given (--version, NEODESK_VERSION) pins it; without one
# the install runs latest, the newest release (DECISIONS A163)
NEODESK_VERSION_GIVEN="${NEODESK_VERSION:-}"
NEODESK_VERSION="${NEODESK_VERSION:-3.6.8}"
NEODESK_RAW_BASE="${NEODESK_RAW_BASE:-https://raw.githubusercontent.com/${NEODESK_REPO}/${NEODESK_BRANCH}}"
INSTALL_DIR="${INSTALL_DIR:-/opt/neodesk}"
INSTALL_MODE="docker"
MIGRATE_SPLIT=false
RELAY_MODE="${RELAY_MODE:-auto}"
RELAY_SERVERS="${RELAY_SERVERS:-}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
NEODESK_DOMAIN="${NEODESK_DOMAIN:-}"
LETSENCRYPT_EMAIL="${LETSENCRYPT_EMAIL:-}"
LETSENCRYPT_STAGING=false
SKIP_DOCKER_INSTALL=false
SKIP_FIREWALL=false
DO_UNINSTALL=false
DO_PURGE=false
DO_RESCUE=false
RESCUE_ACTION="rescue"
CHECK_CAPABILITIES=false
MANAGED_CONFIG_CHANGES=()

# Docker ports: web console 5000 and 5443, API 21121, CDAP 21122, signal and relay
DOCKER_PORTS="21115 21116 21117 21118 21119 21121 21122 5000 5443"
# With --domain: Traefik's HTTP (certificate, redirect) and HTTPS instead
# of the console's own ports, which then answer on this host only
DOCKER_PORTS_TRAEFIK="21115 21116 21117 21118 21119 21121 21122 80 443"

# ── Output helpers ────────────────────────────────────────────────────────────

# Colours: 24-bit where the terminal says it has them (COLORTERM), else the
# 16 basic ones; none when the output is not a terminal, NO_COLOR is set or
# TERM is dumb (COLOR_DEPTH 24bit, 16 or empty). In 24-bit, C_RED, C_GREEN,
# C_YELLOW and C_CYAN keep at least 3.8:1 on black, on PowerShell's blue
# and on white.
COLOR_DEPTH=""
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-}" != dumb ]; then
    case "${COLORTERM:-}" in
        truecolor|24bit) COLOR_DEPTH=24bit ;;
        *) COLOR_DEPTH=16 ;;
    esac
fi
case "$COLOR_DEPTH" in
    24bit)
        C_RED='\033[38;2;228;75;69m'
        C_GREEN='\033[38;2;37;149;46m'
        C_YELLOW='\033[38;2;178;116;9m'
        C_CYAN='\033[38;2;26;143;147m'
        ;;
    16)
        C_RED='\033[0;31m'
        C_GREEN='\033[0;32m'
        C_YELLOW='\033[0;33m'
        C_CYAN='\033[0;36m'
        ;;
    *)
        C_RED='' C_GREEN='' C_YELLOW='' C_CYAN=''
        ;;
esac
if [ -n "$COLOR_DEPTH" ]; then
    C_RESET='\033[0m'
    C_BOLD='\033[1m'
else
    C_RESET='' C_BOLD=''
fi

log()  { echo -e "${C_CYAN}[install]${C_RESET} $*"; }
ok()   { echo -e "${C_GREEN}✓${C_RESET} $*"; }
warn() { echo -e "${C_YELLOW}!${C_RESET} $*" >&2; }
die()  { echo -e "${C_RED}✗${C_RESET} $*" >&2; exit 1; }

# The NeoDesk logo, drawn in half blocks: 15 lines with colour tokens
# {N}; ND_LOGO_24[N] and ND_LOGO_16[N] are the token's codes in 24-bit
# and in the 16 colours.
ND_LOGO_LINES=(
    '             {0}▄▄███████▄▄'
    '           {0}▄█████████████▄'
    '         {0}▄█████████████████▄'
    '        {0}▄███████████████████▄                                                  {1}▄▄▄                          ▄▄▄'
    '  {2}▄▄████{3}█████████{4}▀▀{0}██████████                                                  {1}███                          ███'
    ' {2}███████{3}████████████{0}█████████    {1}▄▄▄▄▄ ▄▄▄▄▄     ▄▄▄▄▄▄▄     ▄▄▄▄▄▄▄     ▄▄▄▄▄▄███     ▄▄▄▄▄▄▄    ▄▄▄▄▄▄▄▄  ███    ▄▄▄▄'
    '{2}████████{3}█████████████{0}████████    {1}██████▀▀▀███▄ ▄███▀▀▀███▄ ▄███▀▀▀███▄ ▄███▀▀▀████   ▄███▀▀▀███▄ ███▀▀▀▀███ ███  ▄███▀'
    '{2}█████████{3}████████████{0}███████       {1}███     ███ ███▄▄▄▄▄███ ███     ███ ███     ███   ███▄▄▄▄▄███ ███▄▄▄▄▄▄  ███▄███▀'
    '{2}██████████{5}▀{3}██████████{0}█████▀        {1}███     ███ ███▀▀▀▀▀▀▀▀ ███     ███ ███     ███   ███▀▀▀▀▀▀▀▀ ▀█████████ ███▀▀███▄'
    '{2}████████████{5}▀{3}████████{0}███▀          {1}███     ███ ███▄   ▄▄▄▄ ███▄   ▄███ ███▄   ▄███▄▄ ███▄   ▄▄▄▄ ▄▄▄    ███ ███  ▀███▄'
    '{2}███████████████{5}▀▀▀▀▀▀{0}▀             {1}███     ███  ▀███████▀   ▀███████▀   ▀█████▀█████  ▀███████▀  ▀████████▀ ███   ▀███▄'
    '{2}█████████████████████'
    '{2}▀███████████████████▀'
    ' {2}▀█████████████████▀'
    '    {2}▀▀▀▀▀▀▀▀▀▀▀▀▀'
)
ND_LOGO_24=('38;2;163;230;53' '38;2;228;231;231' '38;2;88;166;255' '38;2;63;208;196' '38;2;163;230;53;48;2;63;208;196' '38;2;63;208;196;48;2;88;166;255')
ND_LOGO_16=('92' '39' '96' '36' '92;46' '36;106')

# draw_logo TITLE: the NeoDesk logo, with TITLE beside the mark on the
# second line under the wordmark, starting under the foot of the n. A
# terminal narrower than the logo gets the name and TITLE on one line.
draw_logo() {
    local cols=${COLUMNS:-} codes line i n
    [ -n "$cols" ] || cols=$(tput cols 2>/dev/null) || cols=''
    case $cols in '' | *[!0-9]* | 0) cols=80 ;; esac
    if [ "$cols" -lt 119 ]; then
        printf '\033[1mNeoDesk\033[0m %s\n' "$1"
        return 0
    fi
    if [ "$COLOR_DEPTH" = 24bit ]; then codes=("${ND_LOGO_24[@]}"); else codes=("${ND_LOGO_16[@]}"); fi
    for ((i = 0; i < ${#ND_LOGO_LINES[@]}; i++)); do
        line=${ND_LOGO_LINES[i]}
        for ((n = 0; n < ${#codes[@]}; n++)); do
            line=${line//"{$n}"/$'\033[0m\033['"${codes[n]}m"}
        done
        line+=$'\033[0m'
        if [ "$i" = 12 ]; then line+="              $1"; fi
        printf '%s\n' "$line"
    done
}

# print_header: the NeoDesk logo with what this script is
# and the release it installs; one plain line without colour (no
# terminal, NO_COLOR). The managers this script runs leave theirs out
# (NEODESK_HEADER_SHOWN).
print_header() {
    local title="Installer ${NEODESK_VERSION}"
    export NEODESK_HEADER_SHOWN=1
    if [ -z "$COLOR_DEPTH" ]; then
        printf 'NeoDesk %s\n' "$title"
        return 0
    fi
    printf '\n'
    draw_logo "$title"
    printf '\n'
}

usage() {
    sed -n '4,/^#   --help/p' "$0" | sed 's/^# \?//'
    exit 0
}

# ── Argument parsing ──────────────────────────────────────────────────────────

while [ $# -gt 0 ]; do
    case "$1" in
        --docker) INSTALL_MODE="docker"; shift ;;
        --migrate-split) MIGRATE_SPLIT=true; shift ;;
        --split) die "--split is gone: NeoDesk runs as one container. An existing split install moves with --migrate-split" ;;
        --native) INSTALL_MODE="native"; shift ;;
        --install-dir) INSTALL_DIR="$2"; shift 2 ;;
        --version) NEODESK_VERSION="$2"; NEODESK_VERSION_GIVEN="$2"; shift 2 ;;
        --branch) NEODESK_BRANCH="$2"; NEODESK_RAW_BASE="https://raw.githubusercontent.com/${NEODESK_REPO}/${NEODESK_BRANCH}"; shift 2 ;;
        --channel)
            case "${2:-}" in
                stable|main|dev|development) RELEASE_CHANNEL="$2" ;;
                *) die "--channel must be stable or dev" ;;
            esac
            shift 2 ;;
        --relay-mode) RELAY_MODE="$2"; shift 2 ;;
        --relay-servers) RELAY_SERVERS="$2"; shift 2 ;;
        --admin-password) ADMIN_PASSWORD="$2"; shift 2 ;;
        --domain) NEODESK_DOMAIN="$2"; shift 2 ;;
        --letsencrypt-email) LETSENCRYPT_EMAIL="$2"; shift 2 ;;
        --letsencrypt-staging) LETSENCRYPT_STAGING=true; shift ;;
        --skip-docker-install) SKIP_DOCKER_INSTALL=true; shift ;;
        --skip-firewall) SKIP_FIREWALL=true; shift ;;
        --rescue) DO_RESCUE=true; RESCUE_ACTION="rescue"; shift ;;
        --diagnose|--diagnostics) DO_RESCUE=true; RESCUE_ACTION="diagnose"; shift ;;
        --repair-permissions) DO_RESCUE=true; RESCUE_ACTION="repair-permissions"; shift ;;
        --check-permissions|--check-capabilities) CHECK_CAPABILITIES=true; shift ;;
        --set-config)
            [ $# -ge 2 ] && [[ "$2" == *=* ]] || die "--set-config requires KEY=VALUE"
            MANAGED_CONFIG_CHANGES+=("$2")
            shift 2
            ;;
        --purge) DO_PURGE=true; shift ;;
        --uninstall) DO_UNINSTALL=true; shift ;;
        -h|--help) usage ;;
        *) die "Unknown option: $1 (use --help)" ;;
    esac
done

# ── Preflight ─────────────────────────────────────────────────────────────────

require_root() {
    if [ "$(id -u)" -ne 0 ]; then
        die "This installer must run as root. Re-run with: sudo bash"
    fi
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

get_public_ip() {
    local ip
    ip=$(curl -4 -fsS --max-time 5 https://checkip.amazonaws.com 2>/dev/null | tr -d '[:space:]') && [ -n "$ip" ] && echo "$ip" && return
    ip=$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null | tr -d '[:space:]') && [ -n "$ip" ] && echo "$ip" && return
    ip=$(curl -4 -fsS --max-time 5 https://ifconfig.me/ip 2>/dev/null | tr -d '[:space:]') && [ -n "$ip" ] && echo "$ip" && return
    echo "127.0.0.1"
}

get_local_ip() {
    local ip
    ip=$(ip -4 route get 1.1.1.1 2>/dev/null | grep -oP 'src \K[0-9.]+' | head -1)
    [ -n "$ip" ] && echo "$ip" && return
    ip=$(ip -4 addr show scope global 2>/dev/null | grep -oP 'inet \K[0-9.]+' | head -1)
    [ -n "$ip" ] && echo "$ip" && return
    ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    [ -n "$ip" ] && echo "$ip" && return
    echo "127.0.0.1"
}

is_private_ip() {
    case "$1" in
        127.*|10.*|192.168.*) return 0 ;;
        172.1[6-9].*|172.2[0-9].*|172.3[0-1].*) return 0 ;;
        *) return 1 ;;
    esac
}

resolve_relay_address() {
    if [ -n "$RELAY_SERVERS" ]; then
        warn "Using fixed relay address: $RELAY_SERVERS"
        echo "$RELAY_SERVERS"
        return
    fi

    local ip
    case "${RELAY_MODE}" in
        local|lan)
            ip=$(get_local_ip)
            log "Relay mode 'local': using LAN IP $ip"
            ;;
        public|wan)
            ip=$(get_public_ip)
            log "Relay mode 'public': using public IP $ip"
            ;;
        auto|*)
            ip=$(get_public_ip)
            if is_private_ip "$ip"; then
                warn "Auto-detected private/loopback IP: $ip"
                warn "Internet clients may fail relay connections. Set --relay-servers YOUR.PUBLIC.IP or use --relay-mode local for LAN-only."
            fi
            ;;
    esac

    # Include relay port when only an IP was detected.
    case "$ip" in
        *:*) echo "$ip" ;;
        *) echo "${ip}:21117" ;;
    esac
}

fetch_url_to_file() {
    local url="$1"
    local dest="$2"
    local min_bytes="${3:-512}"
    local max_bytes="${4:-1048576}"

    if ! curl -fsSL --max-time 120 --proto '=https' --tlsv1.2 -o "$dest" "$url"; then
        die "Failed to download: $url"
    fi

    local size
    size=$(stat -c%s "$dest" 2>/dev/null || wc -c <"$dest")
    if [ "${size:-0}" -lt "$min_bytes" ] || [ "${size:-0}" -gt "$max_bytes" ]; then
        die "Downloaded file has unexpected size (${size} bytes): $url"
    fi
}

# ── Docker helpers ────────────────────────────────────────────────────────────

check_docker() {
    command -v docker >/dev/null 2>&1 || return 1
    docker info >/dev/null 2>&1 || return 2
    return 0
}

check_docker_compose() {
    if docker compose version >/dev/null 2>&1; then
        COMPOSE_CMD=(docker compose)
        return 0
    fi
    if command -v docker-compose >/dev/null 2>&1; then
        COMPOSE_CMD=(docker-compose)
        return 0
    fi
    return 1
}

# ufw_active: ufw is installed and on. "ufw status" prints "Status:
# active" or "Status: inactive" (in English with LC_ALL=C); rules added to
# an inactive ufw go live with the next "ufw enable".
ufw_active() {
    command -v ufw >/dev/null 2>&1 && LC_ALL=C ufw status 2>/dev/null | grep -q '^Status: active'
}

configure_firewall() {
    [ "$SKIP_FIREWALL" = true ] && { log "Skipping firewall configuration"; return 0; }

    local port created=0
    local docker_ports="$DOCKER_PORTS"

    log "Configuring firewall (if active)..."

    if ufw_active; then
        for port in $docker_ports; do
            if [ "$port" = "21116" ]; then
                ufw allow 21116/tcp comment "NeoDesk signal TCP" >/dev/null 2>&1 && created=$((created + 1)) || true
                ufw allow 21116/udp comment "NeoDesk signal UDP" >/dev/null 2>&1 && created=$((created + 1)) || true
            elif ! ufw status 2>/dev/null | grep -qE "^${port}[/ ]"; then
                ufw allow "${port}/tcp" comment "NeoDesk port ${port}" >/dev/null 2>&1 && created=$((created + 1)) || true
            fi
        done
        ufw reload >/dev/null 2>&1 || true
    elif command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld 2>/dev/null; then
        for port in $docker_ports; do
            if [ "$port" = "21116" ]; then
                firewall-cmd --permanent --add-port=21116/tcp >/dev/null 2>&1 && created=$((created + 1)) || true
                firewall-cmd --permanent --add-port=21116/udp >/dev/null 2>&1 && created=$((created + 1)) || true
            else
                firewall-cmd --permanent --add-port="${port}/tcp" >/dev/null 2>&1 && created=$((created + 1)) || true
            fi
        done
        firewall-cmd --reload >/dev/null 2>&1 || true
    else
        log "No active UFW/firewalld detected — skipping firewall rules"
        return 0
    fi

    if [ "$created" -gt 0 ]; then
        ok "Created $created firewall rule(s)"
    else
        ok "Firewall rules already present"
    fi
}

# A two-container (split) install in the compose folder: its .env says so,
# or its compose file runs a console service.
is_split_install() {
    local compose_dir="$1"
    grep -qs '^NEODESK_DOCKER_LAYOUT=split' "${compose_dir}/.env" \
        || grep -Eqs 'ghcr\.io/[a-z0-9._-]+/neodesk-console' "${compose_dir}/docker-compose.yml"
}

# fetch_docker_manager DEST: neodesk-docker.sh of this installer's
# branch, which installs, updates and removes NeoDesk on Docker.
fetch_docker_manager() {
    local dest="$1"
    mkdir -p "$(dirname "$dest")"
    fetch_url_to_file "${NEODESK_RAW_BASE}/neodesk-docker.sh" "$dest.tmp" 4096 524288
    if ! grep -q 'NEODESK_DOCKER_SOURCED' "$dest.tmp"; then
        rm -f "$dest.tmp"
        die "The downloaded neodesk-docker.sh is not NeoDesk's Docker manager (${NEODESK_RAW_BASE})"
    fi
    install -m 0755 "$dest.tmp" "$dest"
    rm -f "$dest.tmp"
}

# install_docker_mode: neodesk-docker.sh in the compose folder installs
# the one container there (or updates the install it finds), with the
# options given here; a split install is taken over (--adopt) on its data.
install_docker_mode() {
    local compose_dir="${INSTALL_DIR}/docker"
    local manager="${compose_dir}/neodesk-docker.sh"
    local args=(install --dir "$compose_dir" --yes)

    log "NeoDesk Docker installer v${VERSION}: neodesk-docker.sh installs the one container in ${compose_dir}"
    if is_split_install "$compose_dir"; then
        if [ "$MIGRATE_SPLIT" != true ]; then
            warn "This is a split install (server and console containers). NeoDesk now runs as one container."
        fi
        warn "Its data moves into the one container (neodesk-docker.sh --adopt); the old containers are kept, stopped and renamed."
        args+=(--adopt)
    elif [ "$MIGRATE_SPLIT" = true ]; then
        log "No split install in ${compose_dir}; installing the one container"
    fi
    if [ -n "$RELAY_SERVERS" ]; then
        args+=(--relay "$RELAY_SERVERS")
    else
        args+=(--relay-mode "$RELAY_MODE")
    fi
    [ -z "$NEODESK_VERSION_GIVEN" ] || args+=(--tag "$NEODESK_VERSION_GIVEN")
    [ "$SKIP_DOCKER_INSTALL" != true ] || args+=(--no-install-docker)
    if [ -n "$NEODESK_DOMAIN" ]; then
        args+=(--domain "$NEODESK_DOMAIN")
        DOCKER_PORTS="$DOCKER_PORTS_TRAEFIK"
    fi
    [ -z "$LETSENCRYPT_EMAIL" ] || args+=(--email "$LETSENCRYPT_EMAIL")
    [ "$LETSENCRYPT_STAGING" != true ] || args+=(--letsencrypt-staging)

    fetch_docker_manager "$manager"
    ok "Docker manager: $manager"
    export ADMIN_PASSWORD NEODESK_RAW_BASE
    "$manager" "${args[@]}"
    configure_firewall
    log "Later: sudo bash $manager (update), status, logs, backup, uninstall (see --help)"
}

rescue_docker_mode() {
    local rescue_dir="${INSTALL_DIR}/rescue"
    local rescue_script="${rescue_dir}/neodesk-docker.sh"
    local rescue_flag

    case "$RESCUE_ACTION" in
        diagnose) rescue_flag="--diagnose" ;;
        repair-permissions) rescue_flag="--repair-permissions" ;;
        rescue|*) rescue_flag="--rescue" ;;
    esac

    log "Preparing NeoDesk Docker rescue toolkit..."
    require_command curl
    if [ "$RESCUE_ACTION" != "diagnose" ]; then
        check_docker || die "Docker is not available or the daemon is not running"
        check_docker_compose || die "Docker Compose plugin is not available"
    fi

    fetch_docker_manager "$rescue_script"
    ok "Rescue script ready: $rescue_script"

    log "Running Docker rescue action: $RESCUE_ACTION"
    DATA_DIR="${DATA_DIR:-}" COMPOSE_FILE="${COMPOSE_FILE:-}" \
        "$rescue_script" "$rescue_flag" --dir "${INSTALL_DIR}/docker"
}

uninstall_docker_mode() {
    local compose_dir="${INSTALL_DIR}/docker"
    local compose_file="${compose_dir}/docker-compose.yml"
    local manager="${compose_dir}/neodesk-docker.sh"

    require_root
    # The manager of the install removes it and says what it leaves
    if [ -x "$manager" ]; then
        local args=(uninstall --dir "$compose_dir")
        [ "$DO_PURGE" != true ] || args+=(--purge --yes)
        "$manager" "${args[@]}"
        return
    fi
    check_docker_compose || die "Docker Compose not available"

    if [ ! -f "$compose_file" ]; then
        die "No Docker installation found at ${compose_dir}"
    fi

    log "Stopping NeoDesk Docker stack..."
    if [ "$DO_PURGE" = true ]; then
        (cd "$compose_dir" && "${COMPOSE_CMD[@]}" --env-file .env down -v)
        ok "Containers and volumes removed"
    else
        (cd "$compose_dir" && "${COMPOSE_CMD[@]}" --env-file .env down)
        ok "Containers stopped (volumes preserved — re-run with --purge to delete data)"
    fi
}

# ── Native install ────────────────────────────────────────────────────────────

native_backup_source() {
    local source_dir="$1"
    local backup_dir="${source_dir}.backup.$(date +%Y%m%d%H%M%S).$$"

    while [ -e "$backup_dir" ]; do
        backup_dir="${source_dir}.backup.$(date +%Y%m%d%H%M%S).${RANDOM}"
    done
    if ! mv "$source_dir" "$backup_dir"; then
        print_error "Could not preserve existing native source at ${source_dir}"
        return 1
    fi
    printf '%s\n' "$backup_dir"
}

native_clone_source() {
    local repo_dir="$1"
    local backup_dir="${2:-}"
    local staging_parent
    local staging_repo

    mkdir -p "$INSTALL_DIR"
    staging_parent=$(mktemp -d "${INSTALL_DIR}/.source-clone.XXXXXX")
    staging_repo="${staging_parent}/source"

    if ! git clone --depth 1 --branch "$NEODESK_BRANCH" \
        "https://github.com/${NEODESK_REPO}.git" "$staging_repo"; then
        rm -rf "$staging_parent"
        if [ -n "$backup_dir" ] && [ ! -e "$repo_dir" ]; then
            mv "$backup_dir" "$repo_dir" 2>/dev/null || true
        fi
        return 1
    fi

    # The native update path keeps local Go runtime data beside the source.
    # Carry it forward without replacing anything created by the fresh clone.
    if [ -n "$backup_dir" ] \
        && [ -d "$backup_dir/neodesk-server/data" ] \
        && [ ! -e "$staging_repo/neodesk-server/data" ]; then
        if ! cp -a "$backup_dir/neodesk-server/data" "$staging_repo/neodesk-server/"; then
            rm -rf "$staging_parent"
            if [ -n "$backup_dir" ]; then
                mv "$backup_dir" "$repo_dir" 2>/dev/null || true
            fi
            return 1
        fi
    fi

    if ! mv "$staging_repo" "$repo_dir"; then
        rm -rf "$staging_parent"
        if [ -n "$backup_dir" ]; then
            mv "$backup_dir" "$repo_dir" 2>/dev/null || true
        fi
        return 1
    fi
    rm -rf "$staging_parent"
    return 0
}

install_native_mode() {
    local repo_dir="${INSTALL_DIR}/source"
    local relay
    local commit_sha
    local source_backup=""
    local checkout_error=""

    log "NeoDesk native installer v${VERSION}"
    log "Native install branch: ${NEODESK_BRANCH} (override with --branch)"

    require_command git
    relay=$(resolve_relay_address)

    if [ -d "$repo_dir/.git" ]; then
        log "Updating existing clone in ${repo_dir} to ${NEODESK_BRANCH}..."
        git -C "$repo_dir" remote set-url origin "https://github.com/${NEODESK_REPO}.git" || true
        git -C "$repo_dir" fetch --depth 1 origin "$NEODESK_BRANCH"
        # Shallow clones previously pinned to another branch need a branch switch.
        checkout_error=$(mktemp)
        if git -C "$repo_dir" checkout -B "$NEODESK_BRANCH" "FETCH_HEAD" 2>"$checkout_error"; then
            rm -f "$checkout_error"
        else
            warn "Could not switch existing clone to ${NEODESK_BRANCH}."
            if [ -n "$(git -C "$repo_dir" status --short 2>/dev/null || true)" ]; then
                warn "The existing clone contains local changes; preserving it before recloning."
                cat "$checkout_error" >&2 || true
            else
                warn "Git checkout failed; preserving the existing clone before recloning."
                cat "$checkout_error" >&2 || true
            fi
            rm -f "$checkout_error"
            if ! source_backup=$(native_backup_source "$repo_dir"); then
                die "Cannot preserve ${repo_dir}; refusing to remove the existing native source"
            fi
            if ! native_clone_source "$repo_dir" "$source_backup"; then
                die "Could not clone ${NEODESK_REPO}@${NEODESK_BRANCH}; original source preserved at ${source_backup}"
            fi
            warn "Previous native source preserved at ${source_backup}"
        fi
    else
        log "Cloning ${NEODESK_REPO} (${NEODESK_BRANCH})..."
        if [ -e "$repo_dir" ]; then
            if ! source_backup=$(native_backup_source "$repo_dir"); then
                die "Cannot preserve ${repo_dir}; refusing to remove the existing native source"
            fi
            warn "Existing non-Git source preserved at ${source_backup}"
        fi
        if ! native_clone_source "$repo_dir" "$source_backup"; then
            die "Could not clone ${NEODESK_REPO}@${NEODESK_BRANCH}; original source preserved at ${source_backup:-none}"
        fi
        if [ -n "$source_backup" ]; then
            warn "Previous native source preserved at ${source_backup}"
        fi
    fi

    commit_sha=$(git -C "$repo_dir" rev-parse --short HEAD 2>/dev/null || echo "unknown")
    log "Using ${NEODESK_REPO}@${NEODESK_BRANCH} (${commit_sha})"

    chmod +x "${repo_dir}/neodesk.sh"

    log "Running neodesk.sh --auto..."
    export RELAY_MODE RELAY_SERVERS="$relay"
    if [ -n "$ADMIN_PASSWORD" ]; then
        export ADMIN_PASSWORD
    fi
    # The release channel: --channel (dev updates from the dev pre-release,
    # stable from the newest release), else neodesk.sh's own choice
    case "$RELEASE_CHANNEL" in
        stable|main) export UPDATE_GITHUB_BRANCH=main ;;
        dev|development) export UPDATE_GITHUB_BRANCH=dev ;;
    esac
    [ -z "${UPDATE_GITHUB_BRANCH:-}" ] || log "Update channel: ${UPDATE_GITHUB_BRANCH}"
    (cd "$repo_dir" && ./neodesk.sh --auto --relay-servers "$relay")

    ok "Native installation finished. See ${repo_dir} for logs and credentials."
}

uninstall_native_mode() {
    local repo_dir="${INSTALL_DIR}/source"
    local native_installer="${repo_dir}/neodesk.sh"

    require_root
    if [ ! -x "$native_installer" ]; then
        die "Native installer not found at ${native_installer}; nothing was removed"
    fi

    log "Running native uninstall (data is preserved unless --purge is supplied)..."
    local args=(--auto --uninstall)
    if [ "$DO_PURGE" = true ]; then
        args+=(--purge)
    fi
    (cd "$repo_dir" && "$native_installer" "${args[@]}")
}

rescue_native_mode() {
    local repo_dir="${INSTALL_DIR}/source"

    if [ "$RESCUE_ACTION" = "repair-permissions" ] && [ -x "$repo_dir/neodesk.sh" ]; then
        "$repo_dir/neodesk.sh" --repair-permissions
        return $?
    fi
    warn "Native rescue is handled by neodesk.sh on the installed host."
    if [ -x "$repo_dir/neodesk.sh" ]; then
        echo "Run the interactive native repair toolkit with:"
        echo "  sudo ${repo_dir}/neodesk.sh"
        echo "Then choose: Repair Installation or Diagnostics."
        return 0
    fi

    die "Native checkout not found at ${repo_dir}. Re-run without --rescue to install, or use --docker for Docker rescue."
}

run_native_management_action() {
    local repo_dir="${INSTALL_DIR}/source"
    local native_installer="${repo_dir}/neodesk.sh"
    [ -x "$native_installer" ] || die "Native NeoDesk manager not found at ${native_installer}"
    local args=()
    [ "$CHECK_CAPABILITIES" = true ] && args+=(--check-permissions)
    [ "$REPAIR_PERMISSIONS" = true ] && args+=(--repair-permissions)
    local change
    for change in "${MANAGED_CONFIG_CHANGES[@]}"; do
        args+=(--set-config "$change")
    done
    "$native_installer" "${args[@]}"
}

run_docker_management_action() {
    local manager="${INSTALL_DIR}/docker/neodesk-docker.sh"
    [ -x "$manager" ] || die "Docker NeoDesk manager not found at ${manager}"
    local args=(--dir "${INSTALL_DIR}/docker")
    [ "$CHECK_CAPABILITIES" = true ] && args+=(--check-permissions)
    [ "$REPAIR_PERMISSIONS" = true ] && args+=(--repair-permissions)
    local change
    for change in "${MANAGED_CONFIG_CHANGES[@]}"; do
        args+=(--set-config "$change")
    done
    "$manager" "${args[@]}"
}

# ── Main ──────────────────────────────────────────────────────────────────────

main() {
    print_header

    if [ "$DO_UNINSTALL" = true ]; then
        case "$INSTALL_MODE" in
            docker) uninstall_docker_mode ;;
            native) uninstall_native_mode ;;
            *) die "Unknown install mode: $INSTALL_MODE" ;;
        esac
        exit 0
    fi

    require_root

    if [ "$CHECK_CAPABILITIES" = true ] || [ "${#MANAGED_CONFIG_CHANGES[@]}" -gt 0 ]; then
        case "$INSTALL_MODE" in
            docker) run_docker_management_action ;;
            native) run_native_management_action ;;
            *) die "Unknown install mode: $INSTALL_MODE" ;;
        esac
        exit $?
    fi

    if [ "$DO_RESCUE" = true ]; then
        case "$INSTALL_MODE" in
            docker) rescue_docker_mode ;;
            native) rescue_native_mode ;;
            *) die "Unknown install mode: $INSTALL_MODE" ;;
        esac
        exit 0
    fi

    case "$INSTALL_MODE" in
        docker) install_docker_mode ;;
        native) install_native_mode ;;
        *) die "Unknown install mode: $INSTALL_MODE" ;;
    esac
}

# BASH_SOURCE[0] is unset when this script is piped into `bash` (the
# documented `curl ... | sudo bash` path), so default it to $0 under nounset.
if [[ "${BASH_SOURCE[0]:-$0}" == "$0" ]]; then
    main "$@"
fi
