#!/bin/bash
#===============================================================================
#
#   BetterDesk Console Manager v3.5.270
#   All-in-One Interactive Tool for Linux
#
#   Features:
#     - Fresh installation: one service, betterdesk-server, that serves the
#       signal and relay server, the API and the web console
#     - Minimal installation (the same server with its web console off)
#     - Update an installation (release binaries checked by SHA-256)
#     - Upgrade an install with the Node.js console in place, and roll it back
#     - Repair/fix issues (enhanced with graceful shutdown)
#     - Validate installation
#     - Backup & restore
#     - Reset admin password
#     - Build & deploy server (rebuild Go binary with rollback)
#     - Full diagnostics
#     - SHA256 binary verification
#     - Auto mode (non-interactive)
#     - Enhanced service management with health verification
#     - Port conflict detection
#     - Fixed ban system (device-specific, not IP-based)
#     - RustDesk Client API (login, address book sync)
#     - TOTP Two-Factor Authentication
#     - SSL/TLS certificate configuration
#     - PostgreSQL database support
#     - SQLite to PostgreSQL migration
#     - CDAP (Custom Device API Protocol) support
#
#   Usage: 
#     Interactive: sudo ./betterdesk.sh
#     Auto mode:   sudo ./betterdesk.sh --auto
#     PostgreSQL:  sudo ./betterdesk.sh --auto --postgresql
#     Update:      sudo ./betterdesk.sh --update
#     Upgrade the Node.js console install: sudo ./betterdesk.sh --update (or --auto)
#     Roll that upgrade back: sudo ./betterdesk.sh --rollback-go
#
#===============================================================================

set -e

# Version
VERSION="3.5.270"
# Bump when installer control-flow changes must apply mid-session after Update (#219).
BETTERDESK_SH_REVISION="20260725-console-start-306"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# This file; empty when the script is piped into bash
BETTERDESK_SELF="${BASH_SOURCE[0]:-}"
# Preserve argv before shift — used to re-exec after installer self-update (#219).
BETTERDESK_ORIG_ARGV=("$@")

# Auto mode flag
AUTO_MODE=false
SKIP_VERIFY=false
MINIMAL_MODE=false
UNINSTALL_MODE=false
PURGE_MODE=false
CHECK_CAPABILITIES=false
REPAIR_PERMISSIONS=false
MANAGED_CONFIG_CHANGES=()
UPDATE_MODE=false
ROLLBACK_GO_MODE=false
CLEANUP_NODE_MODE=false
# Where the server binary comes from: a file (--binary), a build of this
# checkout (--from-source), else the release of the update channel.
SERVER_BINARY_FILE="${SERVER_BINARY_FILE:-}"
FROM_SOURCE=false

# Relay server selection mode:
#   auto   - detect public IP (default, best for internet-facing servers)
#   local  - use the server's LAN IP (best for LAN-only deployments)
#   public - force public IP detection
# RELAY_SERVERS env var (or --relay-servers) always overrides this with a fixed value.
RELAY_MODE="${RELAY_MODE:-auto}"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --auto|-a)
            AUTO_MODE=true
            shift
            ;;
        --skip-verify)
            SKIP_VERIFY=true
            shift
            ;;
        --minimal)
            MINIMAL_MODE=true
            shift
            ;;
        --uninstall)
            UNINSTALL_MODE=true
            shift
            ;;
        --purge)
            PURGE_MODE=true
            shift
            ;;
        --check-permissions|--check-capabilities)
            CHECK_CAPABILITIES=true
            shift
            ;;
        --repair-permissions)
            REPAIR_PERMISSIONS=true
            shift
            ;;
        --set-config)
            if [ $# -lt 2 ] || [[ "$2" != *=* ]]; then
                echo "ERROR: --set-config requires KEY=VALUE"
                exit 1
            fi
            MANAGED_CONFIG_CHANGES+=("$2")
            shift 2
            ;;
        --nodejs|--flask)
            echo "NOTE: $1 is no longer needed: the web console is part of betterdesk-server"
            shift
            ;;
        --update)
            UPDATE_MODE=true
            shift
            ;;
        --rollback-go)
            ROLLBACK_GO_MODE=true
            shift
            ;;
        --cleanup-node)
            CLEANUP_NODE_MODE=true
            shift
            ;;
        --binary)
            if [ $# -lt 2 ] || [ ! -f "$2" ]; then
                echo "ERROR: --binary needs the path of a betterdesk-server binary"
                exit 1
            fi
            SERVER_BINARY_FILE="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
            shift 2
            ;;
        --from-source)
            FROM_SOURCE=true
            shift
            ;;
        --channel)
            case "${2:-}" in
                stable|main) UPDATE_GITHUB_BRANCH=main ;;
                dev|development) UPDATE_GITHUB_BRANCH=dev ;;
                *)
                    echo "ERROR: --channel must be 'stable' or 'dev'"
                    exit 1
                    ;;
            esac
            export UPDATE_GITHUB_BRANCH
            shift 2
            ;;
        --postgresql|--postgres)
            USE_POSTGRESQL=true
            shift
            ;;
        --relay-mode)
            RELAY_MODE="$2"
            if [ "$RELAY_MODE" != "auto" ] && [ "$RELAY_MODE" != "local" ] && [ "$RELAY_MODE" != "lan" ] && [ "$RELAY_MODE" != "public" ] && [ "$RELAY_MODE" != "wan" ]; then
                echo "ERROR: --relay-mode must be 'auto', 'local' (lan) or 'public' (wan)"
                exit 1
            fi
            shift 2
            ;;
        --relay-servers|--relay)
            RELAY_SERVERS="$2"
            shift 2
            ;;
        --protocol)
            PROTOCOL_MODE="$2"
            if [ "$PROTOCOL_MODE" != "http" ] && [ "$PROTOCOL_MODE" != "https" ]; then
                echo "ERROR: --protocol must be 'http' or 'https'"
                exit 1
            fi
            shift 2
            ;;
        --pg-uri)
            POSTGRESQL_URI="$2"
            USE_POSTGRESQL=true
            shift 2
            ;;
        --help|-h)
            echo "BetterDesk Console Manager v$VERSION"
            echo ""
            echo "Usage: sudo ./betterdesk.sh [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --auto, -a       Run in automatic mode (non-interactive); on an install"
            echo "                   with the Node.js console this runs the upgrade (--update)"
            echo "  --update         Update without questions; an install with the Node.js"
            echo "                   console is upgraded in place to the server with its console"
            echo "  --rollback-go    Put the Node.js console and the server from before the"
            echo "                   upgrade back (binaries, services, .env); data stays"
            echo "  --cleanup-node   Delete the Node.js console's code and its disabled service"
            echo "                   after an upgrade"
            echo "  --binary FILE    Install this betterdesk-server binary instead of the release"
            echo "  --channel C      Release channel: 'stable' (default, the newest release) or"
            echo "                   'dev' (the development build); kept for later updates"
            echo "  --from-source    Build betterdesk-server from this checkout (needs Go, and"
            echo "                   Node.js unless web-ui/build exists)"
            echo "  --uninstall      Stop services and remove the native installation"
            echo "  --purge          With --uninstall, also remove data and keys"
            echo "  --check-permissions  Report panel/update/config/restart capabilities"
            echo "  --repair-permissions Repair BetterDesk ownership and service access"
            echo "  --set-config K=V Apply an allowlisted .env setting (repeatable)"
            echo "  --skip-verify    Skip SHA256 verification of binaries"
            echo "  --minimal        Install the server with its web console off"
            echo "  --postgresql     Use PostgreSQL instead of SQLite"
            echo "  --pg-uri URI     PostgreSQL connection URI (implies --postgresql)"
            echo "  --protocol MODE  Set protocol mode: 'http' or 'https'"
            echo "  --relay-mode M   Relay IP selection: 'auto' (public, default), 'local' (LAN), 'public'"
            echo "  --relay-servers IP  Force a fixed relay server address (IP or host[:port])"
            echo "  --help, -h       Show this help message"
            echo ""
            echo "Environment variables:"
            echo "  USE_POSTGRESQL=true     Use PostgreSQL"
            echo "  POSTGRESQL_URI=...      PostgreSQL connection URI"
            echo "  POSTGRESQL_USER=...     PostgreSQL username (default: betterdesk)"
            echo "  POSTGRESQL_PASS=...     PostgreSQL password (auto-generated if empty)"
            echo "  POSTGRESQL_DB=...       PostgreSQL database (default: betterdesk)"
            echo "  POSTGRESQL_HOST=...     PostgreSQL host (default: localhost)"
            echo "  POSTGRESQL_PORT=...     PostgreSQL port (default: 5432)"
            echo "  RELAY_MODE=auto|local|public  Relay IP selection mode (default: auto)"
            echo "  RELAY_SERVERS=...       Force a fixed relay server address (overrides RELAY_MODE)"
            echo "  STORE_ADMIN_CREDENTIALS=true  Persist admin password to .admin_credentials (not recommended)"
            echo "  ADMIN_PASSWORD=...      Set custom admin password (default: auto-generated)"
            echo "  UPDATE_GITHUB_BRANCH=main|dev  Update channel: stable (main) or development (dev)"
            echo "  UPDATE_GITHUB_OWNER=... / UPDATE_GITHUB_REPO=...  Repository the releases come from"
            echo "  RELEASE_BASE_URL=...    Folder URL with the release files (SHA256SUMS and binaries)"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Go server source directory
GO_SERVER_SOURCE="$SCRIPT_DIR/betterdesk-server"

# Minimum Go version required for compilation (must match betterdesk-server/go.mod).
GO_MIN_VERSION="1.26.11"
# Point release downloaded when system Go is missing/outdated (must exist on go.dev/dl).
GO_DOWNLOAD_VERSION="1.26.11"
# Maximum time allowed for the first module download on a native install.
GO_MODULE_DOWNLOAD_TIMEOUT="${GO_MODULE_DOWNLOAD_TIMEOUT:-600}"
# Maximum time allowed to validate the downloaded module graph.
GO_MODULE_GRAPH_TIMEOUT="${GO_MODULE_GRAPH_TIMEOUT:-120}"
# Maximum time allowed to compile the Go server.
GO_BUILD_TIMEOUT="${GO_BUILD_TIMEOUT:-1800}"
# Heartbeat/deadline settings for bounded Go commands.
GO_COMMAND_HEARTBEAT_INTERVAL="${GO_COMMAND_HEARTBEAT_INTERVAL:-15}"
GO_COMMAND_KILL_AFTER="${GO_COMMAND_KILL_AFTER:-15}"
# Short HTTPS probe before go mod download (fail fast on blocked proxy/DNS).
GO_MODULE_PREFLIGHT_TIMEOUT="${GO_MODULE_PREFLIGHT_TIMEOUT:-20}"
# Set when preflight detects IPv4 OK but IPv6 broken (common on GCP VMs).
GO_FORCE_IPV4=0
# Toolchain for native compile (override with BETTERDESK_GOTOOLCHAIN; default local).
BETTERDESK_GOTOOLCHAIN="${BETTERDESK_GOTOOLCHAIN:-local}"

# Default paths (can be overridden by environment variables)
RUSTDESK_PATH="${RUSTDESK_PATH:-}"
CONSOLE_PATH="${CONSOLE_PATH:-}"
CONSOLE_TYPE="none"  # none, go (the console in betterdesk-server), nodejs (the old console)
BACKUP_DIR="${BACKUP_DIR:-/opt/rustdesk-backups}"

# API: the server's own API on GO_API_PORT (21114). The web console inside the
# server listens on CONSOLE_PORT (5000), and on CLIENT_API_PORT (21121) for
# RustDesk clients when API_ENABLED is true.
GO_API_PORT="${GO_API_PORT:-21114}"
CLIENT_API_PORT="${CLIENT_API_PORT:-21121}"
API_PORT="${API_PORT:-$GO_API_PORT}"
STORE_ADMIN_CREDENTIALS="${STORE_ADMIN_CREDENTIALS:-false}"

# The one service. It runs as SERVICE_USER, so the binary lives in the
# server folder, which that user may write: the in-app updater replaces the
# binary by rename and keeps the old one as betterdesk-server.prev.
SERVER_SERVICE="betterdesk-server"
SERVER_UNIT="/etc/systemd/system/betterdesk-server.service"
# This installer as a command, for updates and the uninstall later.
MANAGER_COMMAND="/usr/local/sbin/betterdesk"
SERVICE_USER="betterdesk"
# What the installs with the Node.js console had besides the server: the
# console's unit (disabled and kept for one release after the upgrade), the
# sudoers rule and the root helper the console called.
LEGACY_CONSOLE_SERVICE="betterdesk-console"
LEGACY_CONSOLE_UNIT="/etc/systemd/system/betterdesk-console.service"
LEGACY_SUDOERS_FILE="/etc/sudoers.d/betterdesk-console-updates"
LEGACY_BROKER_DIR="/usr/local/libexec/betterdesk"
# The record of an upgrade from the Node.js console, in the console's data
# folder: {from, to, at, backup}. The rollback reads it.
GO_UPGRADE_RECORD=".go-upgrade.json"

# Database configuration
USE_POSTGRESQL="${USE_POSTGRESQL:-false}"  # true = PostgreSQL, false = SQLite
POSTGRESQL_URI="${POSTGRESQL_URI:-}"       # postgres://user:pass@host:5432/dbname
POSTGRESQL_DB_CREATED=false                # this run created the (empty) database
POSTGRESQL_USER="${POSTGRESQL_USER:-betterdesk}"
POSTGRESQL_PASS="${POSTGRESQL_PASS:-}"
POSTGRESQL_DB="${POSTGRESQL_DB:-betterdesk}"
POSTGRESQL_HOST="${POSTGRESQL_HOST:-localhost}"
POSTGRESQL_PORT="${POSTGRESQL_PORT:-5432}"

# Common installation paths to search
COMMON_RUSTDESK_PATHS=(
    "/opt/betterdesk"
    "/opt/rustdesk"
    "/usr/local/rustdesk"
    "/var/lib/rustdesk"
    "/home/rustdesk"
    "$HOME/rustdesk"
)

COMMON_CONSOLE_PATHS=(
    "/opt/BetterDeskConsole"
    "/opt/betterdesk"
    "/var/lib/betterdesk"
    "$HOME/BetterDeskConsole"
)

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
WHITE='\033[1;37m'
NC='\033[0m' # No Color
BOLD='\033[1m'
DIM='\033[2m'

# Logging
LOG_FILE="/tmp/betterdesk_$(date +%Y%m%d_%H%M%S).log"

#===============================================================================
# Helper Functions
#===============================================================================

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >> "$LOG_FILE"
}

print_header() {
    clear
    echo -e "${CYAN}"
    echo "╔══════════════════════════════════════════════════════════════════╗"
    echo "║                                                                  ║"
    echo "║   ██████╗ ███████╗████████╗████████╗███████╗██████╗              ║"
    echo "║   ██╔══██╗██╔════╝╚══██╔══╝╚══██╔══╝██╔════╝██╔══██╗             ║"
    echo "║   ██████╔╝█████╗     ██║      ██║   █████╗  ██████╔╝             ║"
    echo "║   ██╔══██╗██╔══╝     ██║      ██║   ██╔══╝  ██╔══██╗             ║"
    echo "║   ██████╔╝███████╗   ██║      ██║   ███████╗██║  ██║             ║"
    echo "║   ╚═════╝ ╚══════╝   ╚═╝      ╚═╝   ╚══════╝╚═╝  ╚═╝             ║"
    echo "║                    ██████╗ ███████╗███████╗██╗  ██╗              ║"
    echo "║                    ██╔══██╗██╔════╝██╔════╝██║ ██╔╝              ║"
    echo "║                    ██║  ██║█████╗  ███████╗█████╔╝               ║"
    echo "║                    ██║  ██║██╔══╝  ╚════██║██╔═██╗               ║"
    echo "║                    ██████╔╝███████╗███████║██║  ██╗              ║"
    echo "║                    ╚═════╝ ╚══════╝╚══════╝╚═╝  ╚═╝              ║"
    echo "║                                                                  ║"
    echo "║                  Console Manager v${VERSION}                     ║"
    echo "╚══════════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
}

print_success() { echo -e "${GREEN}✓${NC} $1"; log "SUCCESS: $1"; }
print_error() { echo -e "${RED}✗${NC} $1"; log "ERROR: $1"; }
print_warning() { echo -e "${YELLOW}!${NC} $1"; log "WARNING: $1"; }
print_info() { echo -e "${BLUE}ℹ${NC} $1"; log "INFO: $1"; }
print_step() { echo -e "${MAGENTA}▶${NC} $1"; log "STEP: $1"; }

#===============================================================================
# Management capabilities and allowlisted runtime configuration
#===============================================================================

# The server binary of this install.
server_binary_path() {
    printf '%s\n' "$RUSTDESK_PATH/betterdesk-server"
}

# True when the installed binary carries the commands of the release with
# the console inside (upgrade-env, env merge, config, reset-password, check,
# update). Read from the file: an older binary would take an unknown
# command for its own arguments and start a server.
binary_has_subcommands() {
    local bin="${1:-$(server_binary_path)}"
    [ -x "$bin" ] && grep -a -q -F 'upgrade-env --from FILE' "$bin" 2>/dev/null
}

# run_as_service_user CMD...: runs a command as the service account, with the
# rights the running server has.
run_as_service_user() {
    if [ "$(id -u)" -ne 0 ] || ! id "$SERVICE_USER" &>/dev/null; then
        "$@"
    elif command -v runuser &>/dev/null; then
        runuser -u "$SERVICE_USER" -- "$@"
    else
        setpriv --reuid="$SERVICE_USER" --regid="$SERVICE_USER" --init-groups -- "$@"
    fi
}

# server_cmd [--as-service] COMMAND ARGS...: a command of the installed
# binary with the console's env file. --as-service runs it as the service
# account.
server_cmd() {
    local as_service=false
    if [ "${1:-}" = "--as-service" ]; then
        as_service=true
        shift
    fi
    local bin
    bin="$(server_binary_path)"
    if ! binary_has_subcommands "$bin"; then
        print_error "$bin is not a release with the console inside; update first"
        return 1
    fi
    if [ "$as_service" = true ]; then
        run_as_service_user env BETTERDESK_ENV_FILE="$CONSOLE_PATH/.env" BETTERDESK_CONSOLE_PATH="$CONSOLE_PATH" \
            BETTERDESK_PATH="$RUSTDESK_PATH" "$bin" "$@"
    else
        BETTERDESK_ENV_FILE="$CONSOLE_PATH/.env" BETTERDESK_CONSOLE_PATH="$CONSOLE_PATH" \
            BETTERDESK_PATH="$RUSTDESK_PATH" "$bin" "$@"
    fi
}

check_management_capabilities() {
    if ! binary_has_subcommands; then
        if is_node_era_install; then
            print_info "Management capabilities are checked after the upgrade to the server with its console"
            return 0
        fi
        print_error "Management capability check needs an installed betterdesk-server"
        return 1
    fi
    print_step "Checking BetterDesk management capabilities..."
    server_cmd --as-service config check || {
        print_warning "One or more management capabilities are blocked; see the report above"
        return 1
    }
    print_success "BetterDesk management capabilities are ready"
}

repair_management_permissions() {
    print_step "Repairing BetterDesk management permissions..."
    ensure_service_user || true
    if [ -d "$CONSOLE_PATH" ]; then
        own_by_service_user "$CONSOLE_PATH"
        chmod 700 "$CONSOLE_PATH/data" 2>/dev/null || true
        chmod 600 "$CONSOLE_PATH/.env" 2>/dev/null || true
    fi
    if [ -d "$RUSTDESK_PATH" ]; then
        set_server_folder_permissions
    fi
    systemctl daemon-reload 2>/dev/null || true
    check_management_capabilities || true
}

apply_managed_config() {
    if [ "${#MANAGED_CONFIG_CHANGES[@]}" -eq 0 ]; then
        print_error "No allowlisted configuration changes supplied"
        return 1
    fi
    print_step "Applying allowlisted BetterDesk configuration..."
    server_cmd --as-service config set "${MANAGED_CONFIG_CHANGES[@]}" || return 1
    systemctl daemon-reload 2>/dev/null || true
    systemctl restart "$SERVER_SERVICE" 2>/dev/null || true
    sleep 2
    start_services_with_verification
}

configure_management() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ MANAGEMENT CAPABILITIES ══════════${NC}"
    echo ""
    check_management_capabilities || true
    echo ""
    local _menu_items=(
        $'Repair permissions\tRepair console user, data paths, TLS and broker'
        $'Change .env settings\tApply allowlisted KEY=VALUE pairs and restart'
        $'Recheck capabilities\tRun the read-only capability report again'
        $'Back\tReturn to the main menu'
    )
    local _menu_returns=( 1 2 3 0 )
    menu_choose "BetterDesk Management" "Only allowlisted BetterDesk operations are available"
    case "${MENU_CHOICE:-0}" in
        1)
            repair_management_permissions
            press_enter
            ;;
        2)
            MANAGED_CONFIG_CHANGES=()
            while true; do
                echo -ne "Enter KEY=VALUE (empty to apply): "
                local item
                read -r item
                [ -z "$item" ] && break
                if [[ "$item" != *=* ]]; then
                    print_warning "Use KEY=VALUE"
                else
                    MANAGED_CONFIG_CHANGES+=("$item")
                fi
            done
            apply_managed_config || print_error "Configuration change failed"
            press_enter
            ;;
        3)
            check_management_capabilities || true
            press_enter
            ;;
        *) return ;;
    esac
}

# Without a terminal (piped from curl, cron, CI) nothing waits for a key,
# and a question without an answer (end of input) takes its default.
press_enter() {
    [ -t 0 ] || return 0
    echo ""
    echo -e "${CYAN}Press Enter to continue...${NC}"
    read -r || true
}

confirm() {
    local prompt="${1:-Continue?}"
    echo -e "${YELLOW}${prompt} [y/N]${NC} "
    read -r response || return 1
    [[ "$response" =~ ^[TtYy]$ ]]
}

# default_to_auto_without_terminal: a run without a terminal installs or
# updates without questions, as --auto does.
default_to_auto_without_terminal() {
    if [ "$AUTO_MODE" != true ] && [ ! -t 0 ]; then
        AUTO_MODE=true
        print_info "No terminal: installing or updating without questions (--auto)"
    fi
}

# install_manager_command: this installer as the command betterdesk, for
# updates, repairs and the uninstall later. Run from the command itself
# (an update), it takes the newest one of the update channel; piped from
# curl, it fetches itself from the same place.
install_manager_command() {
    local self="$BETTERDESK_SELF" tmp
    tmp=$(mktemp) || return 0
    if [ -n "$self" ] && [ -f "$self" ] && [ "$(readlink -f "$self")" != "$(readlink -f "$MANAGER_COMMAND" 2>/dev/null)" ]; then
        cp "$self" "$tmp"
    elif ! curl -fsSL --retry 2 --connect-timeout 20 -o "$tmp" \
        "https://raw.githubusercontent.com/${UPDATE_GITHUB_OWNER}/${UPDATE_GITHUB_REPO}/${UPDATE_GITHUB_BRANCH}/betterdesk.sh" \
        || ! grep -q '^BETTERDESK_SH_REVISION=' "$tmp"; then
        rm -f "$tmp"
        [ -x "$MANAGER_COMMAND" ] || print_warning "Could not save the betterdesk command; run this installer again for updates"
        return 0
    fi
    mkdir -p "$(dirname "$MANAGER_COMMAND")"
    install -m 0755 "$tmp" "$MANAGER_COMMAND" && print_info "Manage this install with: sudo $(basename "$MANAGER_COMMAND")"
    rm -f "$tmp"
    return 0
}

#===============================================================================
# Interactive TUI (arrow-key navigable menu) — pure bash, no dependencies
#===============================================================================
# Result of tui_select() is returned in the global TUI_RESULT.
# Returns 0 on selection, 1 when TUI is unavailable (caller falls back to text).
TUI_RESULT=""

# Detect whether the modern arrow-key interface can be used.
tui_available() {
    [ "${BETTERDESK_CLASSIC_MENU:-0}" = "1" ] && return 1
    [ -t 0 ] && [ -t 1 ] || return 1
    return 0
}

# Cleanup helper: always restore the cursor when leaving the TUI.
_tui_restore() { printf '\033[?25h' 2>/dev/null; stty echo 2>/dev/null; }

# tui_select "Title" "Subtitle" item1 item2 ...
# Each item may embed a description after a literal $'\t' (tab).
# Navigation: ↑/↓ or k/j to move, Enter/→ to choose, q/Esc/0 to cancel.
tui_select() {
    local title="$1"; shift
    local subtitle="$1"; shift
    local items=("$@")
    local count=${#items[@]}
    local sel=0 key rest

    if ! tui_available || [ "$count" -eq 0 ]; then
        TUI_RESULT=""
        return 1
    fi

    printf '\033[?25l'                       # hide cursor
    trap '_tui_restore' INT TERM

    clear
    while true; do
        # Build the whole frame in a single buffer, then emit it with one
        # write. Terminals (notably the VS Code integrated terminal with GPU
        # acceleration) drop individual glyphs when a full-screen TUI is redrawn
        # via many separate printf calls after each keypress. One write avoids it.
        local buf=""
        buf+="\033[H"   # move cursor home instead of clearing (less flicker)
        buf+="${CYAN}${BOLD}+--------------------------------------------------------------+${NC}\033[K\n"
        buf+="$(printf "${CYAN}${BOLD}|${NC} ${WHITE}${BOLD}%-60s${NC} ${CYAN}${BOLD}|${NC}" "$title")\033[K\n"
        if [ -n "$subtitle" ]; then
            buf+="$(printf "${CYAN}${BOLD}|${NC} ${DIM}%-60s${NC} ${CYAN}${BOLD}|${NC}" "$subtitle")\033[K\n"
        fi
        buf+="${CYAN}${BOLD}+--------------------------------------------------------------+${NC}\033[K\n"
        buf+="\033[K\n"

        local i label desc pad line
        for i in "${!items[@]}"; do
            label="${items[$i]%%$'\t'*}"
            desc=""
            [[ "${items[$i]}" == *$'\t'* ]] && desc="${items[$i]#*$'\t'}"
            # Manual padding by character count keeps columns aligned reliably.
            pad=$(( 32 - ${#label} ))
            [ "$pad" -lt 1 ] && pad=1
            if [ "$i" -eq "$sel" ]; then
                line="$(printf "  ${GREEN}${BOLD}>${NC} ${GREEN}${BOLD}%s${NC}%*s${DIM}%s${NC}" "$label" "$pad" "" "$desc")"
            else
                line="$(printf "    ${WHITE}%s${NC}%*s${DIM}%s${NC}" "$label" "$pad" "" "$desc")"
            fi
            buf+="${line}\033[K\n"
        done

        buf+="\033[K\n"
        buf+="  ${DIM}Up/Down navigate   Enter select   q/Esc back${NC}\033[K\n"
        buf+="\033[J"   # clear anything below the menu

        printf '%b' "$buf"

        # Read a single keypress (with escape-sequence handling)
        IFS= read -rsn1 key 2>/dev/null
        if [[ "$key" == $'\033' ]]; then
            read -rsn2 -t 0.05 rest 2>/dev/null
            key+="$rest"
        fi

        case "$key" in
            $'\033[A'|'k') sel=$(( (sel - 1 + count) % count )) ;;
            $'\033[B'|'j') sel=$(( (sel + 1) % count )) ;;
            ''|$'\033[C') TUI_RESULT="$sel"; _tui_restore; trap - INT TERM; return 0 ;;  # Enter / →
            'q'|'Q'|'0'|$'\033') TUI_RESULT=""; _tui_restore; trap - INT TERM; return 2 ;;
            [1-9])
                # Numeric shortcut jumps straight to that 1-based entry
                local idx=$(( key - 1 ))
                if [ "$idx" -lt "$count" ]; then
                    TUI_RESULT="$idx"; _tui_restore; trap - INT TERM; return 0
                fi
                ;;
        esac
    done
}

#===============================================================================
# Modern UI helpers shared by every sub-menu
#===============================================================================
# ui_panel_header "Title" "Subtitle"
# Draws a clean ASCII box header (single buffered write to avoid glyph drops).
ui_panel_header() {
    local title="$1" subtitle="$2"
    clear 2>/dev/null || true
    local buf=""
    buf+="${CYAN}${BOLD}+--------------------------------------------------------------+${NC}\n"
    buf+="$(printf "${CYAN}${BOLD}|${NC} ${WHITE}${BOLD}%-60s${NC} ${CYAN}${BOLD}|${NC}" "$title")\n"
    if [ -n "$subtitle" ]; then
        buf+="$(printf "${CYAN}${BOLD}|${NC} ${DIM}%-60s${NC} ${CYAN}${BOLD}|${NC}" "$subtitle")\n"
    fi
    buf+="${CYAN}${BOLD}+--------------------------------------------------------------+${NC}\n"
    printf '%b' "$buf"
    echo ""
}

# ui_section "Title"  — a lightweight section divider for output screens.
ui_section() {
    echo ""
    echo -e "  ${CYAN}${BOLD}== $1 ==${NC}"
    echo ""
}

# menu_choose "Title" "Subtitle"
# Caller must pre-populate two parallel arrays:
#   _menu_items=( "Label\tDescription" ... )   # what the user sees
#   _menu_returns=( "1" "2" ... "0" )          # value returned for each entry
# The chosen value is stored in MENU_CHOICE. On cancel (q/Esc) the LAST entry's
# value is returned (by convention the final item is "Back"/"Exit").
# Uses the arrow-key TUI when available, otherwise a styled numeric menu.
MENU_CHOICE=""
menu_choose() {
    local title="$1" subtitle="$2"
    MENU_CHOICE=""
    local last_idx=$(( ${#_menu_returns[@]} - 1 ))
    [ "$last_idx" -lt 0 ] && last_idx=0

    if tui_available; then
        tui_select "$title" "$subtitle" "${_menu_items[@]}"
        local rc=$?
        if [ "$rc" -eq 0 ] && [ -n "$TUI_RESULT" ]; then
            MENU_CHOICE="${_menu_returns[$TUI_RESULT]}"
        else
            MENU_CHOICE="${_menu_returns[$last_idx]}"
        fi
        return 0
    fi

    # Styled numeric fallback (no TTY / classic mode)
    ui_panel_header "$title" "$subtitle"
    local i label desc
    for i in "${!_menu_items[@]}"; do
        label="${_menu_items[$i]%%$'\t'*}"
        desc=""
        [[ "${_menu_items[$i]}" == *$'\t'* ]] && desc="${_menu_items[$i]#*$'\t'}"
        printf "  ${GREEN}${BOLD}%2s${NC}) ${WHITE}%-28s${NC} ${DIM}%s${NC}\n" \
            "${_menu_returns[$i]}" "$label" "$desc"
    done
    echo ""
    echo -ne "  ${CYAN}Select option:${NC} "
    read -r MENU_CHOICE
}

get_public_ip() {
    local ip
    ip=$(curl -4 -s --max-time 5 ifconfig.me 2>/dev/null) && [ -n "$ip" ] && echo "$ip" && return
    ip=$(curl -4 -s --max-time 5 icanhazip.com 2>/dev/null) && [ -n "$ip" ] && echo "$ip" && return
    ip=$(curl -s --max-time 5 ifconfig.me 2>/dev/null) && [ -n "$ip" ] && echo "$ip" && return
    ip=$(curl -s --max-time 5 icanhazip.com 2>/dev/null) && [ -n "$ip" ] && echo "$ip" && return
    echo "127.0.0.1"
}

# Detect the server's primary LAN/private IPv4 address.
# Used for LAN-only deployments where the public IP is unreachable by clients.
get_local_ip() {
    local ip
    # Primary: source address used to reach the default gateway
    ip=$(ip -4 route get 1.1.1.1 2>/dev/null | grep -oP 'src \K[0-9.]+' | head -1)
    [ -n "$ip" ] && echo "$ip" && return
    # Fallback: first non-loopback global-scope address
    ip=$(ip -4 addr show scope global 2>/dev/null | grep -oP 'inet \K[0-9.]+' | head -1)
    [ -n "$ip" ] && echo "$ip" && return
    # Last resort: hostname resolution
    ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    [ -n "$ip" ] && echo "$ip" && return
    echo "127.0.0.1"
}

# Resolve P2P/relay connection strategy env vars for systemd/NSSM (issue #157).
resolve_connection_mode_env() {
    CONNECTION_MODE="${CONNECTION_MODE:-p2p_first}"
    if [ "$AUTO_MODE" = false ] && [ -z "$CONNECTION_MODE_SET" ]; then
        echo ""
        print_info "Connection strategy: P2P hole punch vs relay-only routing."
        echo -e "  ${CYAN}1)${NC} P2P first (recommended) — try direct connection, fall back to relay"
        echo -e "  ${CYAN}2)${NC} Relay only — route all sessions through the relay server"
        echo -ne "  ${CYAN}Select connection mode [1]:${NC} "
        read -r _conn_choice
        case "$_conn_choice" in
            2) CONNECTION_MODE="relay_only" ;;
            *) CONNECTION_MODE="p2p_first" ;;
        esac
        echo ""
    fi

    P2P_FALLBACK_MS="${P2P_FALLBACK_MS:-2000}"
    SAME_NAT_RELAY="${SAME_NAT_RELAY:-Y}"

    case "$CONNECTION_MODE" in
        relay_only)
            P2P_FIRST_ENV=N
            ALWAYS_USE_RELAY_ENV=Y
            ;;
        *)
            P2P_FIRST_ENV=Y
            ALWAYS_USE_RELAY_ENV=N
            ;;
    esac

    CONNECTION_MODE_ENV_BLOCK="Environment=P2P_FIRST=$P2P_FIRST_ENV
Environment=ALWAYS_USE_RELAY=$ALWAYS_USE_RELAY_ENV
Environment=P2P_FALLBACK_MS=$P2P_FALLBACK_MS
Environment=SAME_NAT_RELAY=$SAME_NAT_RELAY"
    print_info "Connection mode: ${CONNECTION_MODE} (P2P_FIRST=$P2P_FIRST_ENV, ALWAYS_USE_RELAY=$ALWAYS_USE_RELAY_ENV)"
}

# Resolve the relay server address according to RELAY_MODE / RELAY_SERVERS.
# Prints the resolved address to stdout; warnings/info go to stderr so the
# captured value (server_ip=$(resolve_relay_ip)) stays clean.
resolve_relay_ip() {
    # Explicit override always wins
    if [ -n "$RELAY_SERVERS" ]; then
        echo "Using fixed relay address (RELAY_SERVERS): $RELAY_SERVERS" >&2
        echo "$RELAY_SERVERS"
        return
    fi

    local ip
    case "${RELAY_MODE:-auto}" in
        local|lan)
            ip=$(get_local_ip)
            echo "Relay mode 'local': using LAN IP $ip (LAN-only deployment)" >&2
            ;;
        public|wan)
            ip=$(get_public_ip)
            echo "Relay mode 'public': using public IP $ip" >&2
            ;;
        auto|*)
            ip=$(get_public_ip)
            # Warn if auto-detection returned a private/loopback address — relay
            # will not work for remote clients unless this is a LAN-only setup.
            if [ "$ip" = "127.0.0.1" ] || [[ "$ip" == 10.* ]] || [[ "$ip" == 192.168.* ]] || [[ "$ip" == 172.1[6-9].* ]] || [[ "$ip" == 172.2[0-9].* ]] || [[ "$ip" == 172.3[0-1].* ]]; then
                echo "WARNING: Auto-detected private/loopback IP: $ip" >&2
                echo "WARNING: Remote (internet) clients will NOT connect via relay with this address." >&2
                echo "         For LAN-only use, this is fine. For internet access, run with:" >&2
                echo "           --relay-servers YOUR.PUBLIC.IP   (or RELAY_SERVERS env var)" >&2
                echo "         To silence this and use the LAN IP explicitly, run with:" >&2
                echo "           --relay-mode local" >&2
            fi
            ;;
    esac
    echo "$ip"
}

sql_escape_literal() {
    # Escape single quotes for SQL string literals: ' -> ''
    local value="$1"
    printf "%s" "${value//\'/\'\'}"
}

is_valid_pg_identifier() {
    # PostgreSQL unquoted identifier compatible pattern.
    # Keeps installation scripts safe from SQL injection in CREATE/ALTER statements.
    local ident="$1"
    [[ "$ident" =~ ^[A-Za-z_][A-Za-z0-9_]{0,62}$ ]]
}

is_local_postgresql_host() {
    case "${POSTGRESQL_HOST,,}" in
        localhost|127.0.0.1|::1)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

postgresql_hba_file_path() {
    local hba_file
    hba_file=$(sudo -u postgres psql -v ON_ERROR_STOP=1 -Atqc "SHOW hba_file;" 2>/dev/null) || return 1
    hba_file="${hba_file//$'\r'/}"
    [ -n "$hba_file" ] || return 1
    printf '%s\n' "$hba_file"
}

ensure_postgresql_local_hba_rules() {
    if ! is_local_postgresql_host; then
        print_info "PostgreSQL host '$POSTGRESQL_HOST' is remote; leaving pg_hba.conf unchanged"
        return 0
    fi

    local hba_file
    if ! hba_file=$(postgresql_hba_file_path); then
        print_error "Could not determine PostgreSQL pg_hba.conf path"
        return 1
    fi
    if [ ! -f "$hba_file" ]; then
        print_error "PostgreSQL pg_hba.conf was not found at: $hba_file"
        return 1
    fi

    local rule_ipv4="host    $POSTGRESQL_DB    $POSTGRESQL_USER    127.0.0.1/32    scram-sha-256"
    local rule_ipv6="host    $POSTGRESQL_DB    $POSTGRESQL_USER    ::1/128         scram-sha-256"
    local has_ipv4=false
    local has_ipv6=false

    if awk -v db="$POSTGRESQL_DB" -v user="$POSTGRESQL_USER" '
        $1 !~ /^#/ && $1 == "host" && $2 == db && $3 == user &&
        $4 == "127.0.0.1/32" && $5 == "scram-sha-256" { found=1 }
        END { exit !found }
    ' "$hba_file"; then
        has_ipv4=true
    fi
    if awk -v db="$POSTGRESQL_DB" -v user="$POSTGRESQL_USER" '
        $1 !~ /^#/ && $1 == "host" && $2 == db && $3 == user &&
        $4 == "::1/128" && $5 == "scram-sha-256" { found=1 }
        END { exit !found }
    ' "$hba_file"; then
        has_ipv6=true
    fi

    if [ "$has_ipv4" = true ] && [ "$has_ipv6" = true ]; then
        print_info "PostgreSQL localhost authentication rules already configured"
        return 0
    fi

    local backup_file="${hba_file}.betterdesk-backup-$(date +%Y%m%d%H%M%S)"
    if ! sudo cp -p "$hba_file" "$backup_file" || ! sudo chmod 600 "$backup_file"; then
        print_error "Could not back up PostgreSQL pg_hba.conf before editing"
        return 1
    fi

    local temp_file
    if ! temp_file=$(mktemp "${TMPDIR:-/tmp}/betterdesk-pg-hba.XXXXXX"); then
        print_error "Could not create a temporary PostgreSQL pg_hba.conf"
        return 1
    fi

    local add_ipv4=0
    local add_ipv6=0
    [ "$has_ipv4" = false ] && add_ipv4=1
    [ "$has_ipv6" = false ] && add_ipv6=1

    if ! awk \
        -v add_ipv4="$add_ipv4" \
        -v add_ipv6="$add_ipv6" \
        -v rule_ipv4="$rule_ipv4" \
        -v rule_ipv6="$rule_ipv6" '
        function add_rules() {
            if (inserted) {
                return
            }
            if (add_ipv4) {
                print rule_ipv4
            }
            if (add_ipv6) {
                print rule_ipv6
            }
            inserted=1
        }
        {
            if ($0 !~ /^[[:space:]]*#/ && $0 !~ /^[[:space:]]*$/) {
                add_rules()
            }
            print
        }
        END {
            if (!inserted) {
                add_rules()
            }
        }
    ' "$hba_file" > "$temp_file"; then
        rm -f "$temp_file"
        print_error "Could not prepare PostgreSQL pg_hba.conf"
        return 1
    fi

    if ! sudo cp "$temp_file" "$hba_file"; then
        rm -f "$temp_file"
        print_error "Could not update PostgreSQL pg_hba.conf"
        print_info "The original file remains available at: $backup_file"
        return 1
    fi
    rm -f "$temp_file"

    if ! systemctl reload postgresql 2>/dev/null && ! service postgresql reload 2>/dev/null; then
        print_error "Could not reload PostgreSQL after updating pg_hba.conf"
        print_info "The original file remains available at: $backup_file"
        return 1
    fi

    print_success "Configured PostgreSQL localhost authentication for BetterDesk"
    return 0
}

#===============================================================================
# Service Management Functions (Enhanced v2.1.2)
#===============================================================================

# Wait for a service to fully stop with timeout
wait_for_service_stop() {
    local service_name="$1"
    local timeout="${2:-30}"
    local elapsed=0
    
    while [ $elapsed -lt $timeout ]; do
        if ! systemctl is-active --quiet "$service_name" 2>/dev/null; then
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    
    print_warning "Service $service_name did not stop within ${timeout}s"
    return 1
}

# Kill any stale processes that might be holding files/ports
# Free BetterDesk ports when systemd stop left orphan listeners (#219).
kill_processes_holding_ports() {
    local port pids
    for port in 21116 21117 5000 5443; do
        if ! ss -tlnH 2>/dev/null | grep -q ":${port} "; then
            continue
        fi
        pids=$(lsof -t -iTCP:"${port}" -sTCP:LISTEN 2>/dev/null || true)
        if [ -n "$pids" ]; then
            print_warning "Port ${port} still in use (pids: $pids) — terminating (#219)"
            for pid in $pids; do
                kill -TERM "$pid" 2>/dev/null || true
            done
            sleep 1
            pids=$(lsof -t -iTCP:"${port}" -sTCP:LISTEN 2>/dev/null || true)
            if [ -n "$pids" ]; then
                for pid in $pids; do
                    kill -9 "$pid" 2>/dev/null || true
                done
            fi
        elif command -v fuser &>/dev/null; then
            fuser -k "${port}/tcp" 2>/dev/null || true
        fi
    done
    sleep 1
}

kill_stale_processes() {
    local process_name="$1"
    # The program itself, as the first word of the command line: a process
    # that only names it in its arguments (this installer run with
    # --binary .../betterdesk-server-linux-amd64) is left alone
    local pattern="^([^ ]*/)?${process_name}( |$)"

    # Find and kill any remaining processes
    local pids
    pids=$(pgrep -f "$pattern" 2>/dev/null || true)

    if [ -n "$pids" ]; then
        print_warning "Found stale $process_name processes: $pids"

        # Try graceful termination first
        for pid in $pids; do
            kill -TERM "$pid" 2>/dev/null || true
        done
        sleep 2

        # Force kill if still running
        pids=$(pgrep -f "$pattern" 2>/dev/null || true)
        if [ -n "$pids" ]; then
            for pid in $pids; do
                kill -9 "$pid" 2>/dev/null || true
            done
            sleep 1
        fi
        
        print_info "Cleaned up stale $process_name processes"
    fi
}

# Check if a port is available
check_port_available() {
    local port="$1"
    local service_name="${2:-unknown}"
    
    if ss -tlnp 2>/dev/null | grep -q ":${port} " || \
       netstat -tlnp 2>/dev/null | grep -q ":${port} "; then
        local process=$(ss -tlnp 2>/dev/null | grep ":${port} " | awk '{print $NF}' || \
                       netstat -tlnp 2>/dev/null | grep ":${port} " | awk '{print $NF}')
        print_error "Port $port is already in use by: $process"
        return 1
    fi
    return 0
}

# Verify that a service is healthy (running and listening on expected port)
_tcp_port_is_listening() {
    local port="$1"
    # Match :PORT followed by whitespace or end — covers ss/netstat layouts
    # like "0.0.0.0:80", "*:80", "[::]:80" (#219).
    ss -tlnH 2>/dev/null | grep -qE ":${port}([[:space:]]|$)" || \
        ss -tln 2>/dev/null | grep -qE ":${port}([[:space:]]|$)" || \
        netstat -tln 2>/dev/null | grep -qE ":${port}([[:space:]]|$)"
}

# Hint when .env requests a privileged panel port but Node bound a fallback (#219).
_hint_panel_privileged_port_mismatch() {
    local expected_port="$1"
    local https_enabled
    https_enabled=$(read_effective_console_setting HTTPS_ENABLED false)

    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" != "true" ]; then
        return 0
    fi

    if [ "$expected_port" = "443" ] && _tcp_port_is_listening 5443; then
        print_info "  Panel is listening on :5443 instead of configured :443"
        print_info "  → Run Repair → Repair permissions (adds CAP_NET_BIND_SERVICE), then restart betterdesk-server"
        print_info "  → Or set HTTPS_PORT=5443 and use a reverse proxy on :443 (docs/setup/REVERSE_PROXY.md)"
        return 0
    fi

    if [ "$expected_port" = "80" ] && _tcp_port_is_listening 5000; then
        print_info "  HTTP redirect is on :5000 instead of configured :80 — set PORT=80 and run Repair → Repair permissions"
    fi
}

verify_service_health() {
    local service_name="$1"
    local expected_port="$2"
    local timeout="${3:-10}"
    local elapsed=0
    
    # First check if service is active
    if ! systemctl is-active --quiet "$service_name" 2>/dev/null; then
        print_error "Service $service_name is not running"
        show_service_logs "$service_name" 20
        return 1
    fi
    
    # If port specified, wait for it to be bound
    if [ -n "$expected_port" ]; then
        while [ $elapsed -lt $timeout ]; do
            if _tcp_port_is_listening "$expected_port"; then
                return 0
            fi
            sleep 1
            elapsed=$((elapsed + 1))
        done
        
        print_error "Service $service_name is running but not listening on port $expected_port"
        if [ "$service_name" = "$SERVER_SERVICE" ] || [ "$service_name" = "$LEGACY_CONSOLE_SERVICE" ]; then
            _hint_panel_privileged_port_mismatch "$expected_port"
        fi
        show_service_logs "$service_name" 20
        return 1
    fi
    
    return 0
}

# Show recent service logs for debugging
show_service_logs() {
    local service_name="$1"
    local lines="${2:-30}"
    
    echo ""
    echo -e "${YELLOW}═══ Recent logs for $service_name ═══${NC}"
    journalctl -u "$service_name" -n "$lines" --no-pager 2>/dev/null || \
        print_warning "Could not retrieve logs for $service_name"
    echo -e "${YELLOW}═══════════════════════════════════════${NC}"
    echo ""
}

# Gracefully stop all BetterDesk services with proper cleanup
graceful_stop_services() {
    print_step "Stopping services gracefully..."
    
    # New Go services (primary)
    local services=("betterdesk-console" "betterdesk-server")
    # Legacy services (for migration)
    local legacy_services=("betterdesk" "rustdesksignal" "rustdeskrelay" "betterdesk-api" "betterdesk-go")
    
    # Stop current services
    for service in "${services[@]}"; do
        if systemctl is-active --quiet "$service" 2>/dev/null; then
            print_info "Stopping $service..."
            systemctl stop "$service" 2>/dev/null || true
        fi
    done
    
    # Stop legacy services if they exist
    for service in "${legacy_services[@]}"; do
        if systemctl is-active --quiet "$service" 2>/dev/null; then
            print_info "Stopping legacy $service..."
            systemctl stop "$service" 2>/dev/null || true
        fi
    done
    
    # Wait for services to stop
    for service in "${services[@]}" "${legacy_services[@]}"; do
        wait_for_service_stop "$service" 15
    done
    
    # Kill any stale processes (Go and legacy Rust)
    kill_stale_processes "betterdesk-server"
    kill_stale_processes "hbbs"
    kill_stale_processes "hbbr"
    kill_processes_holding_ports
    
    print_success "All services stopped"
}

# Read a console setting as the running console sees it. The server with its
# console takes its unit's Environment= first and fills the rest from .env.
# The Node.js console's unit had EnvironmentFile=, which overrides
# Environment=, so there .env comes first (#219).
read_effective_console_setting() {
    local key="$1"
    local default="${2:-}"
    local env_file="${CONSOLE_PATH}/.env"
    local val=""

    if is_go_only_install; then
        val=$(unit_env_value "$SERVER_UNIT" "$key")
        [ -n "$val" ] || val=$(env_file_value "$env_file" "$key")
    else
        val=$(env_file_value "$env_file" "$key")
        if [ -z "$val" ] && [ -f "$LEGACY_CONSOLE_UNIT" ]; then
            val=$(unit_env_value "$LEGACY_CONSOLE_UNIT" "$key")
        fi
    fi
    if [ -z "$val" ]; then
        val="$default"
    fi
    echo "$val"
}

# The service that serves the web console.
console_service_name() {
    if is_go_only_install; then
        echo "$SERVER_SERVICE"
    else
        echo "$LEGACY_CONSOLE_SERVICE"
    fi
}

# set_panel_http_port PORT: the console's HTTP port in .env. The server reads
# CONSOLE_PORT; PORT keeps the same value for the record and a rollback.
set_panel_http_port() {
    local env_file="${CONSOLE_PATH}/.env"
    _upsert_env_line "$env_file" PORT "$1"
    if is_go_only_install || grep -q '^CONSOLE_PORT=' "$env_file" 2>/dev/null; then
        _upsert_env_line "$env_file" CONSOLE_PORT "$1"
    fi
}

# Keep betterdesk-console.service Environment=PORT/HTTPS_PORT aligned with .env (#219).
_sync_console_panel_ports_to_systemd() {
    local svc_file="/etc/systemd/system/betterdesk-console.service"
    local env_file="${CONSOLE_PATH}/.env"
    local http_port https_port changed=0

    is_go_only_install && return 1
    [ -f "$svc_file" ] || return 1

    http_port=$(grep -m1 '^PORT=' "$env_file" 2>/dev/null | cut -d= -f2- | tr -d '[:space:]')
    https_port=$(grep -m1 '^HTTPS_PORT=' "$env_file" 2>/dev/null | cut -d= -f2- | tr -d '[:space:]')
    [ -n "$http_port" ] || http_port="5000"
    [ -n "$https_port" ] || https_port="5443"

    if ! grep -qE "^Environment=PORT=${http_port}$" "$svc_file" 2>/dev/null; then
        _upsert_systemd_env "$svc_file" PORT "$http_port"
        changed=1
    fi
    if [ "$(read_effective_console_setting HTTPS_ENABLED false | tr '[:upper:]' '[:lower:]')" = "true" ]; then
        if ! grep -qE "^Environment=HTTPS_PORT=${https_port}$" "$svc_file" 2>/dev/null; then
            _upsert_systemd_env "$svc_file" HTTPS_PORT "$https_port"
            changed=1
        fi
    fi

    if [ "$changed" -eq 1 ]; then
        systemctl daemon-reload 2>/dev/null || true
        return 0
    fi
    return 1
}

_upsert_env_line() {
    local file="$1" key="$2" value="$3"
    [ -f "$file" ] || touch "$file"
    if grep -q "^${key}=" "$file" 2>/dev/null; then
        sed -i "s|^${key}=.*|${key}=${value}|" "$file"
    else
        echo "${key}=${value}" >> "$file"
    fi
}

_remove_env_line() {
    local file="$1" key="$2"
    [ -f "$file" ] && sed -i "/^${key}=/d" "$file"
}

_upsert_systemd_env() {
    local file="$1" key="$2" value="$3"
    [ -f "$file" ] || return 0
    if grep -q "Environment=${key}=" "$file" 2>/dev/null; then
        sed -i "s|Environment=${key}=.*|Environment=${key}=${value}|" "$file"
    else
        sed -i "/^\[Service\]/a Environment=${key}=${value}" "$file"
    fi
}

_remove_systemd_env() {
    local file="$1" key="$2"
    [ -f "$file" ] && sed -i "/Environment=${key}=/d" "$file"
}

# Infer RUSTDESK_API_TLS / ALLOW_SELF_SIGNED_CERTS from certificate path (LE vs self-signed).
infer_tls_mode_from_cert() {
    local cert_path="$1"
    local resolved
    resolved=$(readlink -f "$cert_path" 2>/dev/null || echo "$cert_path")
    if [[ "$resolved" == *"/etc/letsencrypt/"* ]]; then
        INFERRED_RUSTDESK_API_TLS="true"
        INFERRED_ALLOW_SELF_SIGNED="false"
    else
        INFERRED_RUSTDESK_API_TLS="false"
        INFERRED_ALLOW_SELF_SIGNED="true"
    fi
}

# Resolve Let's Encrypt live/ dir from .env, paths, symlinks, LE_CERT_DOMAIN, or cert SAN (#219).
resolve_le_cert_live_dir() {
    local env_file="${1:-${CONSOLE_PATH}/.env}"
    local cert_hint="${2:-$RUSTDESK_PATH/ssl/betterdesk.crt}"
    local ssl_key_env ssl_cert_env le_live_dir le_domain dns_name

    le_live_dir=$(grep -m1 '^LE_CERT_LIVE_DIR=' "$env_file" 2>/dev/null | cut -d= -f2- || true)
    ssl_cert_env=$(grep -m1 '^SSL_CERT_PATH=' "$env_file" 2>/dev/null | cut -d= -f2- || true)
    ssl_key_env=$(grep -m1 '^SSL_KEY_PATH=' "$env_file" 2>/dev/null | cut -d= -f2- || true)

    if [ -z "$le_live_dir" ] && [[ "$ssl_cert_env" == *"/etc/letsencrypt/"* ]]; then
        le_live_dir=$(dirname "$(readlink -f "$ssl_cert_env" 2>/dev/null || echo "$ssl_cert_env")")
    fi
    if [ -z "$le_live_dir" ] && [[ "$ssl_key_env" == *"/etc/letsencrypt/"* ]]; then
        le_live_dir=$(dirname "$(readlink -f "$ssl_key_env" 2>/dev/null || echo "$ssl_key_env")")
    fi
    if [ -z "$le_live_dir" ] && [ -L "$cert_hint" ]; then
        le_live_dir=$(dirname "$(readlink -f "$cert_hint" 2>/dev/null || echo "")")
    fi
    if [ -z "$le_live_dir" ]; then
        le_domain=$(grep -m1 '^LE_CERT_DOMAIN=' "$env_file" 2>/dev/null | cut -d= -f2- || true)
        if [ -n "$le_domain" ] && [ -d "/etc/letsencrypt/live/$le_domain" ]; then
            le_live_dir="/etc/letsencrypt/live/$le_domain"
        fi
    fi
    if [ -z "$le_live_dir" ] && [ -f "$cert_hint" ] && command -v openssl &>/dev/null; then
        dns_name=$(openssl x509 -in "$cert_hint" -noout -ext subjectAltName 2>/dev/null | \
            grep -oE 'DNS:[^, ]+' | head -1 | cut -d: -f2- || true)
        if [ -n "$dns_name" ] && [ -d "/etc/letsencrypt/live/$dns_name" ]; then
            le_live_dir="/etc/letsencrypt/live/$dns_name"
        fi
    fi
    echo "$le_live_dir"
}

# Copy a TLS file to dest as a real file (not a symlink).
# - If dest is already the same real file as src (self-signed generated in place),
#   do nothing — deleting dest would remove src and break cp (#325, discussion #322).
# - If dest is a symlink that resolves to src (LE live dir), remove the symlink
#   and copy content so the console user can read it (#219).
_safe_cp_tls_file() {
    local src="$1"
    local dest="$2"
    local src_real dest_real tmp

    [ -f "$src" ] || return 1
    src_real=$(readlink -f "$src" 2>/dev/null || echo "$src")
    if [ -e "$dest" ]; then
        dest_real=$(readlink -f "$dest" 2>/dev/null || echo "$dest")
        if [ "$src_real" = "$dest_real" ]; then
            if [ -L "$dest" ]; then
                # Symlink to src → replace with a real copy (#219)
                rm -f "$dest"
            else
                # Already a real file at dest (self-signed path) — no copy needed (#325)
                return 0
            fi
        fi
    fi
    tmp="${dest}.betterdesk.$$.tmp"
    cp -L "$src" "$tmp" || return 1
    mv -f "$tmp" "$dest" || { rm -f "$tmp"; return 1; }
    return 0
}

# The server reads PORT as its signal port when SIGNAL_PORT is missing, and
# the console's .env sets PORT for the console (#219): .env names the
# signal and relay ports (the admin's values stay), and the API port stays
# on the unit. While an install runs the Node.js console, the server's own
# console stays off (CONSOLE_PORT=0): Node holds its port.
ensure_go_server_signal_ports() {
    local go_svc_file="$SERVER_UNIT"
    local changed=0
    local go_api_port="${GO_API_PORT:-21114}"

    [ -f "$go_svc_file" ] || return 1

    if is_node_era_install && ! grep -q '^Environment=CONSOLE_PORT=' "$go_svc_file" 2>/dev/null; then
        _upsert_systemd_env "$go_svc_file" CONSOLE_PORT 0
        changed=1
    fi
    if [ -f "$CONSOLE_PATH/.env" ]; then
        if [ -z "$(env_file_value "$CONSOLE_PATH/.env" SIGNAL_PORT)" ] && [ -z "$(unit_env_value "$go_svc_file" SIGNAL_PORT)" ]; then
            _upsert_env_line "$CONSOLE_PATH/.env" SIGNAL_PORT 21116
            changed=1
        fi
        if [ -z "$(env_file_value "$CONSOLE_PATH/.env" RELAY_PORT)" ] && [ -z "$(unit_env_value "$go_svc_file" RELAY_PORT)" ]; then
            _upsert_env_line "$CONSOLE_PATH/.env" RELAY_PORT 21117
            changed=1
        fi
    fi
    if ! grep -q "^Environment=GO_API_PORT=${go_api_port}" "$go_svc_file" 2>/dev/null; then
        _upsert_systemd_env "$go_svc_file" GO_API_PORT "$go_api_port"
        changed=1
    fi
    if [ -f "$go_svc_file" ] && grep -qE '\-api-port[[:space:]]+21121\b' "$go_svc_file" 2>/dev/null; then
        print_info "Migrating Go -api-port 21121 → ${go_api_port} (handlers on Go; clients stay on :${CLIENT_API_PORT:-21121} proxy)"
        sed -i "s/-api-port 21121/-api-port ${go_api_port}/" "$go_svc_file"
        changed=1
    fi
    if [ "$changed" -eq 1 ]; then
        systemctl daemon-reload 2>/dev/null || true
        print_info "Server ports: signal $(signal_port), relay $(relay_port), API ${go_api_port} (PORT and API_PORT in .env are the console's)"
    fi
    [ "$changed" -eq 1 ]
}

# Copy TLS material into $RUSTDESK_PATH/ssl/ as real files (not symlinks) so the
# betterdesk console user can read them. LE live dirs are root-only (#219).
deploy_ssl_material_to_rustdesk_dir() {
    local cert_src="$1"
    local key_src="$2"
    local le_live_dir="${3:-}"
    local ssl_dir="$RUSTDESK_PATH/ssl"
    local svc_user="betterdesk"
    local env_file="${CONSOLE_PATH}/.env"

    if [ ! -f "$cert_src" ] || [ ! -f "$key_src" ]; then
        print_error "Certificate or key source not found: cert=$cert_src key=$key_src"
        return 1
    fi

    mkdir -p "$ssl_dir"
    if ! _safe_cp_tls_file "$cert_src" "$ssl_dir/betterdesk.crt"; then
        print_error "Failed to deploy certificate to $ssl_dir/betterdesk.crt"
        return 1
    fi
    if ! _safe_cp_tls_file "$key_src" "$ssl_dir/betterdesk.key"; then
        print_error "Failed to deploy private key to $ssl_dir/betterdesk.key"
        return 1
    fi

    if id "$svc_user" &>/dev/null; then
        chown root:"$svc_user" "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key" 2>/dev/null || true
    fi
    chmod 640 "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key" 2>/dev/null || true

    if [ -n "$le_live_dir" ]; then
        _upsert_env_line "$env_file" LE_CERT_LIVE_DIR "$le_live_dir"
        _upsert_env_line "$env_file" LE_CERT_DOMAIN "$(basename "$le_live_dir")"
        install_le_certbot_renew_hook
    fi

    return 0
}

# certbot deploy hook: re-copy renewed LE certs then restart BetterDesk services.
install_le_certbot_renew_hook() {
    local hook_dir="/etc/letsencrypt/renewal-hooks/deploy"
    local hook="$hook_dir/betterdesk-reload.sh"
    local conf="$hook_dir/betterdesk-reload.conf"
    mkdir -p "$hook_dir"

    cat > "$conf" <<EOF
RUSTDESK_PATH=${RUSTDESK_PATH}
CONSOLE_PATH=${CONSOLE_PATH}
EOF
    chmod 640 "$conf"

    cat > "$hook" <<'HOOK'
#!/bin/bash
set -euo pipefail
CONF="/etc/letsencrypt/renewal-hooks/deploy/betterdesk-reload.conf"
[ -f "$CONF" ] && . "$CONF"
RUSTDESK_PATH="${RUSTDESK_PATH:-/opt/rustdesk}"
CONSOLE_PATH="${CONSOLE_PATH:-/opt/betterdesk}"
ENV_FILE="$CONSOLE_PATH/.env"
SSL_DIR="$RUSTDESK_PATH/ssl"
SVC_USER="betterdesk"

le_live_dir=""
if [ -f "$ENV_FILE" ]; then
    le_live_dir=$(grep -m1 '^LE_CERT_LIVE_DIR=' "$ENV_FILE" 2>/dev/null | cut -d= -f2- || true)
fi
if [ -z "$le_live_dir" ] || [ ! -d "$le_live_dir" ]; then
    echo "betterdesk-reload: LE_CERT_LIVE_DIR missing or invalid — skipping cert copy" >&2
    systemctl restart betterdesk-server 2>/dev/null || true
    exit 0
fi

mkdir -p "$SSL_DIR"
for pair in "fullchain.pem:betterdesk.crt" "privkey.pem:betterdesk.key"; do
    src_name="${pair%%:*}"
    dest_name="${pair##*:}"
    src="$le_live_dir/$src_name"
    dest="$SSL_DIR/$dest_name"
    src_real=$(readlink -f "$src" 2>/dev/null || echo "$src")
    if [ -e "$dest" ]; then
        dest_real=$(readlink -f "$dest" 2>/dev/null || echo "$dest")
        [ "$src_real" = "$dest_real" ] && rm -f "$dest"
    fi
    tmp="${dest}.betterdesk.$$.tmp"
    cp -L "$src" "$tmp"
    mv -f "$tmp" "$dest"
done
if id "$SVC_USER" &>/dev/null; then
    chown root:"$SVC_USER" "$SSL_DIR/betterdesk.crt" "$SSL_DIR/betterdesk.key"
fi
chmod 640 "$SSL_DIR/betterdesk.crt" "$SSL_DIR/betterdesk.key"
systemctl restart betterdesk-server 2>/dev/null || true
HOOK
    chmod +x "$hook"
}

# Repair installs that symlinked LE certs into ssl/ (console user cannot read privkey).
maybe_repair_le_ssl_symlinks() {
    local ssl_dir="$RUSTDESK_PATH/ssl"
    local crt="$ssl_dir/betterdesk.crt"
    local key="$ssl_dir/betterdesk.key"
    local env_file="${CONSOLE_PATH}/.env"
    local needs_redeploy="no"
    local ssl_key_env ssl_cert_env
    local console_user="betterdesk"

    for f in "$crt" "$key"; do
        if [ -L "$f" ]; then
            local target
            target=$(readlink -f "$f" 2>/dev/null || readlink "$f" 2>/dev/null || echo "")
            if [[ "$target" == *"/etc/letsencrypt/"* ]]; then
                needs_redeploy="yes"
            fi
        fi
    done

    ssl_key_env=$(grep -m1 '^SSL_KEY_PATH=' "$env_file" 2>/dev/null | cut -d= -f2- || true)
    ssl_cert_env=$(grep -m1 '^SSL_CERT_PATH=' "$env_file" 2>/dev/null | cut -d= -f2- || true)
    if [[ "$ssl_key_env" == *"/etc/letsencrypt/"* ]] || [[ "$ssl_cert_env" == *"/etc/letsencrypt/"* ]]; then
        needs_redeploy="yes"
    fi
    if id "$console_user" &>/dev/null && [ -e "$key" ] \
        && ! runuser -u "$console_user" -- test -r "$key" 2>/dev/null; then
        needs_redeploy="yes"
    fi

    if [ "$needs_redeploy" != "yes" ]; then
        return 2
    fi

    local le_live_dir
    le_live_dir=$(resolve_le_cert_live_dir "$env_file" "$crt")

    if [ -z "$le_live_dir" ] || [ ! -f "$le_live_dir/fullchain.pem" ] || [ ! -f "$le_live_dir/privkey.pem" ]; then
        print_warning "LE certificate symlinks detected but live dir not found — manual repair may be needed"
        return 1
    fi

    print_info "Repairing LE certificate symlinks → copied files for console user (#219)"
    if ! deploy_ssl_material_to_rustdesk_dir "$le_live_dir/fullchain.pem" "$le_live_dir/privkey.pem" "$le_live_dir"; then
        print_error "LE redeploy failed for $le_live_dir — remove symlinks under $ssl_dir and retry"
        return 1
    fi
    _sync_deployed_ssl_paths_to_env
    return 0
}

# When HTTPS uses standard port 443, align HTTP redirect listener to :80 (#219).
_ensure_standard_https_redirect_ports() {
    local env_file="${CONSOLE_PATH}/.env"
    local svc_file="/etc/systemd/system/betterdesk-console.service"
    local https_port http_port https_enabled changed=0
    local svc_port svc_https

    https_enabled=$(read_effective_console_setting HTTPS_ENABLED false)
    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" != "true" ]; then
        return 1
    fi

    https_port=$(read_effective_console_setting HTTPS_PORT 5443)
    [ "$https_port" = "443" ] || return 1

    http_port=$(resolve_panel_http_port)
    if [ "$http_port" != "80" ]; then
        set_panel_http_port 80
        _upsert_env_line "$env_file" HTTP_REDIRECT_HTTPS true
        changed=1
    fi
    if is_go_only_install; then
        # Ports below 1024 need CAP_NET_BIND_SERVICE in the unit
        [ "$changed" -eq 1 ] && maybe_update_services >/dev/null
    elif [ -f "$svc_file" ]; then
        svc_port=$(grep -E '^Environment=PORT=' "$svc_file" 2>/dev/null | tail -1 | sed 's/^Environment=PORT=//')
        svc_https=$(grep -E '^Environment=HTTPS_PORT=' "$svc_file" 2>/dev/null | tail -1 | sed 's/^Environment=HTTPS_PORT=//')
        if [ "$svc_port" != "80" ] || [ "$svc_https" != "443" ]; then
            _upsert_systemd_env "$svc_file" HTTPS_PORT 443
            _upsert_systemd_env "$svc_file" PORT 80
            _upsert_systemd_env "$svc_file" HTTP_REDIRECT_HTTPS true
            systemctl daemon-reload 2>/dev/null || true
            changed=1
        fi
    fi
    if [ "$changed" -eq 1 ]; then
        ensure_service_user >/dev/null
        print_info "Standard HTTPS ports synced: HTTPS :443, HTTP redirect :80 (#219)"
        return 0
    fi
    return 1
}

# Repair HTTPS stuck state: Go signal port isolation + LE material redeploy (#219).
repair_https_stuck_state() {
    local quiet="${1:-}"
    local changed=0

    if ensure_go_server_signal_ports; then
        changed=1
    fi

    local https_enabled
    https_enabled=$(read_effective_console_setting HTTPS_ENABLED false)
    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" = "true" ]; then
        local le_rc=2
        maybe_repair_le_ssl_symlinks || le_rc=$?
        if [ "$le_rc" -eq 0 ]; then
            changed=1
        elif [ "$le_rc" -ne 2 ] && ensure_console_tls_material_readable; then
            changed=1
        fi
        _sync_deployed_ssl_paths_to_env 2>/dev/null || true
        if _ensure_standard_https_redirect_ports; then
            changed=1
        fi
        if _sync_console_panel_ports_to_systemd; then
            changed=1
        fi
    fi

    if [ "$changed" -eq 1 ] && [ "$quiet" != "yes" ]; then
        print_info "HTTPS/TLS configuration repaired (#219)"
    fi
    return 0
}

# Ensure copied TLS files under $RUSTDESK_PATH/ssl/ are referenced in .env + systemd.
_sync_deployed_ssl_paths_to_env() {
    local ssl_dir="$RUSTDESK_PATH/ssl"
    local env_file="${CONSOLE_PATH}/.env"
    local svc_file="/etc/systemd/system/betterdesk-console.service"

    _upsert_env_line "$env_file" SSL_CERT_PATH "$ssl_dir/betterdesk.crt"
    _upsert_env_line "$env_file" SSL_KEY_PATH "$ssl_dir/betterdesk.key"
    if [ -f "$svc_file" ]; then
        _upsert_systemd_env "$svc_file" SSL_CERT_PATH "$ssl_dir/betterdesk.crt"
        _upsert_systemd_env "$svc_file" SSL_KEY_PATH "$ssl_dir/betterdesk.key"
        systemctl daemon-reload 2>/dev/null || true
    fi
}

# When HTTPS is enabled, ensure the console user can read the TLS private key (#219).
# Re-copies from LE_CERT_LIVE_DIR when symlinks, unreadable keys, or /etc/letsencrypt paths remain.
ensure_console_tls_material_readable() {
    local https_enabled console_user="betterdesk"
    local env_file="${CONSOLE_PATH}/.env"
    local ssl_dir="$RUSTDESK_PATH/ssl"
    local ssl_key_path ssl_cert_path

    https_enabled=$(read_effective_console_setting HTTPS_ENABLED false)
    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" != "true" ]; then
        return 0
    fi

    maybe_repair_le_ssl_symlinks 2>/dev/null || true

    ssl_key_path=$(read_effective_console_setting SSL_KEY_PATH "")
    ssl_cert_path=$(read_effective_console_setting SSL_CERT_PATH "")
    if [ -z "$ssl_key_path" ]; then
        ssl_key_path="$ssl_dir/betterdesk.key"
    fi

    if id "$console_user" &>/dev/null && [ -e "$ssl_key_path" ]; then
        if runuser -u "$console_user" -- test -r "$ssl_key_path" 2>/dev/null; then
            return 0
        fi
    fi

    local le_live_dir
    le_live_dir=$(resolve_le_cert_live_dir "$env_file" "$ssl_key_path")
    if [ -z "$le_live_dir" ] && [ -n "$ssl_cert_path" ]; then
        le_live_dir=$(resolve_le_cert_live_dir "$env_file" "$ssl_cert_path")
    fi

    if [ -n "$le_live_dir" ] && [ -f "$le_live_dir/fullchain.pem" ] && [ -f "$le_live_dir/privkey.pem" ]; then
        print_info "Re-deploying Let's Encrypt certificate for console user (#219)"
        if deploy_ssl_material_to_rustdesk_dir "$le_live_dir/fullchain.pem" "$le_live_dir/privkey.pem" "$le_live_dir"; then
            _sync_deployed_ssl_paths_to_env
            if id "$console_user" &>/dev/null && runuser -u "$console_user" -- test -r "$ssl_dir/betterdesk.key" 2>/dev/null; then
                return 0
            fi
        fi
    fi

    print_warning "HTTPS is enabled but console user cannot read TLS key (${ssl_key_path:-$ssl_dir/betterdesk.key})"
    print_info "  Check: runuser -u betterdesk -- test -r ${ssl_key_path:-$ssl_dir/betterdesk.key}"
    print_info "  Logs:  journalctl -u betterdesk-server -n 30 --no-pager"
    return 1
}

# Wait for an HTTP(S) endpoint to return a usable status code (post-restart boot delay).
_wait_for_http_code() {
    local url="$1"
    local max_wait="${2:-15}"
    local use_insecure="${3:-}"
    local elapsed=0
    local code="000"
    local curl_args=(-s -o /dev/null -w '%{http_code}' --max-time 4)

    if [ "$use_insecure" = "yes" ]; then
        curl_args=(-k "${curl_args[@]}")
    fi

    while [ "$elapsed" -lt "$max_wait" ]; do
        code=$(curl "${curl_args[@]}" "$url" 2>/dev/null || echo "000")
        if [[ "$code" =~ ^(200|301|302|304|401|403|405)$ ]]; then
            echo "$code"
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    echo "$code"
    return 1
}

# Sync .env + betterdesk-console.service for HTTP or HTTPS panel mode (#219).
apply_console_protocol_mode() {
    local mode="$1"
    local cert_crt="${2:-}"
    local cert_key="${3:-}"
    local api_tls="${4:-false}"
    local allow_self_signed="${5:-true}"
    local env_file="${CONSOLE_PATH}/.env"
    local svc_file="/etc/systemd/system/betterdesk-console.service"
    local go_port="${GO_API_PORT:-21114}"

    if [ "$mode" = "http" ]; then
        _upsert_env_line "$env_file" HTTPS_ENABLED false
        _upsert_env_line "$env_file" RUSTDESK_API_TLS false
        _upsert_env_line "$env_file" ALLOW_SELF_SIGNED_CERTS false
        _upsert_env_line "$env_file" HTTP_REDIRECT_HTTPS false
        _upsert_env_line "$env_file" TRUST_PROXY false
        _upsert_env_line "$env_file" HBBS_API_URL "http://localhost:${go_port}/api"
        _upsert_env_line "$env_file" BETTERDESK_API_URL "http://localhost:${go_port}/api"
        _remove_env_line "$env_file" NODE_EXTRA_CA_CERTS
        _remove_env_line "$env_file" ENTERPRISE_TLS

        if [ -f "$svc_file" ]; then
            _upsert_systemd_env "$svc_file" HTTPS_ENABLED false
            _upsert_systemd_env "$svc_file" RUSTDESK_API_TLS false
            _upsert_systemd_env "$svc_file" ALLOW_SELF_SIGNED_CERTS false
            _upsert_systemd_env "$svc_file" HTTP_REDIRECT_HTTPS false
            _upsert_systemd_env "$svc_file" TRUST_PROXY false
            sed -i "s|Environment=HBBS_API_URL=https://localhost|Environment=HBBS_API_URL=http://localhost|" "$svc_file"
            sed -i "s|Environment=BETTERDESK_API_URL=https://localhost|Environment=BETTERDESK_API_URL=http://localhost|" "$svc_file"
            _remove_systemd_env "$svc_file" NODE_EXTRA_CA_CERTS
            _remove_systemd_env "$svc_file" ENTERPRISE_TLS
        fi
        sync_go_server_trust_proxy no
    elif [ "$mode" = "https" ]; then
        _upsert_env_line "$env_file" HTTPS_ENABLED true
        _upsert_env_line "$env_file" SSL_CERT_PATH "$cert_crt"
        _upsert_env_line "$env_file" SSL_KEY_PATH "$cert_key"
        _upsert_env_line "$env_file" TRUST_PROXY false
        if ! grep -q '^HTTPS_PORT=' "$env_file" 2>/dev/null; then
            _upsert_env_line "$env_file" HTTPS_PORT 5443
        fi
        _upsert_env_line "$env_file" HTTP_REDIRECT_HTTPS true
        _upsert_env_line "$env_file" RUSTDESK_API_TLS "$api_tls"
        _upsert_env_line "$env_file" ALLOW_SELF_SIGNED_CERTS "$allow_self_signed"
        _upsert_env_line "$env_file" HBBS_API_URL "http://localhost:${go_port}/api"
        _upsert_env_line "$env_file" BETTERDESK_API_URL "http://localhost:${go_port}/api"
        # Only the Node.js console read NODE_EXTRA_CA_CERTS
        _remove_env_line "$env_file" NODE_EXTRA_CA_CERTS

        if [ -f "$svc_file" ]; then
            _upsert_systemd_env "$svc_file" HTTPS_ENABLED true
            _upsert_systemd_env "$svc_file" SSL_CERT_PATH "$cert_crt"
            _upsert_systemd_env "$svc_file" SSL_KEY_PATH "$cert_key"
            _upsert_systemd_env "$svc_file" HTTP_REDIRECT_HTTPS true
            _upsert_systemd_env "$svc_file" RUSTDESK_API_TLS "$api_tls"
            _upsert_systemd_env "$svc_file" ALLOW_SELF_SIGNED_CERTS "$allow_self_signed"
            _upsert_systemd_env "$svc_file" TRUST_PROXY false
            sed -i "s|Environment=HBBS_API_URL=https://localhost|Environment=HBBS_API_URL=http://localhost|" "$svc_file"
            sed -i "s|Environment=BETTERDESK_API_URL=https://localhost|Environment=BETTERDESK_API_URL=http://localhost|" "$svc_file"
            if [ "$allow_self_signed" = "true" ]; then
                _upsert_systemd_env "$svc_file" NODE_EXTRA_CA_CERTS "$cert_crt"
            else
                _remove_systemd_env "$svc_file" NODE_EXTRA_CA_CERTS
            fi
        fi
        sync_go_server_trust_proxy no
    else
        print_error "apply_console_protocol_mode: unknown mode '$mode'"
        return 1
    fi

    systemctl daemon-reload 2>/dev/null || true
}

# Enable signal/relay TLS on betterdesk-server using deployed panel cert (#219).
sync_go_server_signal_relay_tls() {
    local ssl_dir="${1:-$RUSTDESK_PATH/ssl}"
    local go_svc_file="$SERVER_UNIT"

    [ -f "$go_svc_file" ] || return 0
    sed -i 's/ -tls-cert [^ ]*//g' "$go_svc_file"
    sed -i 's/ -tls-key [^ ]*//g' "$go_svc_file"
    sed -i 's/ -tls-signal//g' "$go_svc_file"
    sed -i 's/ -tls-relay//g' "$go_svc_file"
    sed -i 's/ -tls-api//g' "$go_svc_file"
    sed -i 's/ -force-https//g' "$go_svc_file"
    sed -i "s|\(ExecStart=.*betterdesk-server[^$]*\)|\1 -tls-cert $ssl_dir/betterdesk.crt -tls-key $ssl_dir/betterdesk.key -tls-signal -tls-relay|" "$go_svc_file"
    systemctl daemon-reload 2>/dev/null || true
}

# Remove signal/relay TLS from betterdesk-server (#219).
clear_go_server_signal_relay_tls() {
    local go_svc_file="$SERVER_UNIT"

    [ -f "$go_svc_file" ] || return 0
    sed -i 's/ -tls-cert [^ ]*//g' "$go_svc_file"
    sed -i 's/ -tls-key [^ ]*//g' "$go_svc_file"
    sed -i 's/ -tls-signal//g' "$go_svc_file"
    sed -i 's/ -tls-relay//g' "$go_svc_file"
    sed -i 's/ -tls-api//g' "$go_svc_file"
    sed -i 's/ -force-https//g' "$go_svc_file"
    systemctl daemon-reload 2>/dev/null || true
}

# Enable or disable Go server reverse-proxy trust (#267 / #276).
# Optional 2nd arg: TRUSTED_PROXIES CIDR list. Omitted → loopback default.
# Empty string → enable TRUST_PROXY but do not write TRUSTED_PROXIES (remote proxy).
sync_go_server_trust_proxy() {
    local enable="${1:-yes}"
    local trusted_cidrs
    if [ "$#" -ge 2 ]; then
        trusted_cidrs="$2"
    else
        trusted_cidrs="127.0.0.1/32,::1/128"
    fi
    local go_svc_file="$SERVER_UNIT"

    [ -f "$go_svc_file" ] || return 0
    if [ "$enable" = "yes" ]; then
        _upsert_systemd_env "$go_svc_file" TRUST_PROXY Y
        if [ -n "$trusted_cidrs" ]; then
            _upsert_systemd_env "$go_svc_file" TRUSTED_PROXIES "$trusted_cidrs"
        fi
        if ! grep -q '\-trust-proxy' "$go_svc_file" 2>/dev/null; then
            sed -i 's|\(ExecStart=.*betterdesk-server[^$]*\)|\1 -trust-proxy|' "$go_svc_file"
        fi
    else
        _remove_systemd_env "$go_svc_file" TRUST_PROXY
        _remove_systemd_env "$go_svc_file" TRUSTED_PROXIES
        sed -i 's/ -trust-proxy//g' "$go_svc_file"
    fi
    systemctl daemon-reload 2>/dev/null || true
}

# Console + Go settings for TLS termination at an external reverse proxy (#267).
apply_console_reverse_proxy_mode() {
    local panel_host="${1:-}"
    local server_id="${2:-}"
    local ws_origins="${3:-}"
    local panel_bind="${4:-127.0.0.1}"
    local env_file="${CONSOLE_PATH}/.env"
    local svc_file="/etc/systemd/system/betterdesk-console.service"
    local go_port="${GO_API_PORT:-21114}"

    apply_console_protocol_mode http
    clear_go_server_signal_relay_tls

    _upsert_env_line "$env_file" HOST "$panel_bind"
    _upsert_env_line "$env_file" TRUST_PROXY Y
    # Same-host proxy: loopback. Remote proxy (HOST=0.0.0.0): operator must set the proxy CIDR.
    local trusted_cidrs="127.0.0.1/32,::1/128"
    if [ "$panel_bind" = "0.0.0.0" ]; then
        trusted_cidrs=""
    fi
    if [ -n "$trusted_cidrs" ]; then
        _upsert_env_line "$env_file" TRUSTED_PROXIES "$trusted_cidrs"
    fi
    _upsert_env_line "$env_file" HTTP_REDIRECT_HTTPS false
    _upsert_env_line "$env_file" HBBS_API_URL "http://localhost:${go_port}/api"
    _upsert_env_line "$env_file" BETTERDESK_API_URL "http://localhost:${go_port}/api"

    if [ -n "$panel_host" ]; then
        _upsert_env_line "$env_file" PANEL_PUBLIC_HOST "$panel_host"
        _upsert_env_line "$env_file" PANEL_PUBLIC_URL "https://${panel_host}"
    fi
    if [ -n "$server_id" ]; then
        _upsert_env_line "$env_file" PUBLIC_SERVER_ID "$server_id"
    elif [ -n "$panel_host" ]; then
        _upsert_env_line "$env_file" PUBLIC_SERVER_ID "$panel_host"
    fi
    if [ -n "$ws_origins" ]; then
        _upsert_env_line "$env_file" WS_ALLOWED_ORIGINS "$ws_origins"
    elif [ -n "$panel_host" ]; then
        _upsert_env_line "$env_file" WS_ALLOWED_ORIGINS "https://${panel_host}"
    fi

    if [ -f "$svc_file" ]; then
        _upsert_systemd_env "$svc_file" HOST "$panel_bind"
        _upsert_systemd_env "$svc_file" TRUST_PROXY Y
        _upsert_systemd_env "$svc_file" HTTPS_ENABLED false
        _upsert_systemd_env "$svc_file" HTTP_REDIRECT_HTTPS false
        _upsert_systemd_env "$svc_file" RUSTDESK_API_TLS false
    fi

    sync_go_server_trust_proxy yes "$trusted_cidrs"
    systemctl daemon-reload 2>/dev/null || true
}

# Write Caddy/Nginx snippets and verify script under $RUSTDESK_PATH/reverse-proxy/ (#267).
generate_reverse_proxy_config() {
    local panel_host="${1:-}"
    local proxy_type="${2:-caddy}"
    local route_wss="${3:-yes}"
    local server_id="${4:-}"
    local panel_bind="${5:-}"
    local upstream_addr="${6:-}"

    if [ -z "$panel_host" ]; then
        read -p "Public panel hostname (e.g., console.example.com): " panel_host
        if [ -z "$panel_host" ]; then
            print_error "Hostname is required for reverse-proxy snippets"
            return 1
        fi
    fi

    if [ -z "$panel_bind" ]; then
        if confirm "Is the reverse proxy on THIS server (same host as BetterDesk)?"; then
            panel_bind="127.0.0.1"
            upstream_addr="127.0.0.1"
        else
            panel_bind="0.0.0.0"
            upstream_addr=$(ip route get 1 2>/dev/null | awk '{print $7; exit}')
            [ -z "$upstream_addr" ] && upstream_addr=$(hostname -I 2>/dev/null | awk '{print $1}')
            echo ""
            read -p "BetterDesk LAN IP for proxy upstream [${upstream_addr}]: " _custom_up
            [ -n "$_custom_up" ] && upstream_addr="$_custom_up"
            if [ -z "$upstream_addr" ]; then
                print_error "LAN IP required when the proxy runs on another host"
                return 1
            fi
            print_warning "Panel will listen on 0.0.0.0:5000 — restrict firewall to your proxy host"
        fi
    fi
    [ -z "$upstream_addr" ] && upstream_addr="$panel_bind"
    if [ "$panel_bind" = "0.0.0.0" ] && [ "$upstream_addr" = "0.0.0.0" ]; then
        upstream_addr=$(ip route get 1 2>/dev/null | awk '{print $7; exit}')
    fi

    if [ -z "$proxy_type" ] || [ "$proxy_type" = "prompt" ]; then
        echo ""
        echo "  1) Caddy"
        echo "  2) Nginx"
        read -p "Proxy type [1]: " _proxy_pick
        case "${_proxy_pick:-1}" in
            2) proxy_type="nginx" ;;
            *) proxy_type="caddy" ;;
        esac
    fi

    if [ -z "$route_wss" ] || [ "$route_wss" = "prompt" ]; then
        if confirm "Route RustDesk WSS paths (/ws/id, /ws/relay) on the same hostname?"; then
            route_wss="yes"
        else
            route_wss="no"
        fi
    fi

    if [ -z "$server_id" ]; then
        if confirm "Use a different hostname for RustDesk ID/relay clients than the panel?"; then
            read -p "RustDesk ID server hostname (e.g., desk.example.com): " server_id
        fi
    fi
    [ -z "$server_id" ] && server_id="$panel_host"

    local out_dir="$RUSTDESK_PATH/reverse-proxy"
    mkdir -p "$out_dir"

    local ws_origins="https://${panel_host}"
    [ "$panel_host" != "$server_id" ] && ws_origins="${ws_origins},https://${server_id}"

    cat > "$out_dir/betterdesk.env.snippet" << EOF
# BetterDesk reverse-proxy mode (#267) — merge into $CONSOLE_PATH/.env
HOST=${panel_bind}
HTTPS_ENABLED=false
HTTP_REDIRECT_HTTPS=false
TRUST_PROXY=Y
TRUSTED_PROXIES=127.0.0.1/32,::1/128
CONSOLE_PORT=5000
PORT=5000
PANEL_PUBLIC_HOST=${panel_host}
PANEL_PUBLIC_URL=https://${panel_host}
PUBLIC_SERVER_ID=${server_id}
WS_ALLOWED_ORIGINS=${ws_origins}
EOF

    if [ "$proxy_type" = "nginx" ]; then
        cat > "$out_dir/nginx.betterdesk.conf.snippet" << EOF
# BetterDesk reverse-proxy snippet (#267) — merge into your nginx site config.
# TLS certificates: use certbot --nginx or your existing cert setup.

map \$http_upgrade \$connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    server_name ${panel_host};

    client_max_body_size 100M;
EOF
        if [ "$route_wss" = "yes" ]; then
            cat >> "$out_dir/nginx.betterdesk.conf.snippet" << EOF

    location = /ws/id {
        proxy_pass http://${upstream_addr}:21118;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "Upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_buffering off;
        proxy_read_timeout 120s;
        proxy_send_timeout 120s;
    }

    location = /ws/relay {
        proxy_pass http://${upstream_addr}:21119;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "Upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_buffering off;
        proxy_read_timeout 120s;
        proxy_send_timeout 120s;
    }
EOF
        fi
        cat >> "$out_dir/nginx.betterdesk.conf.snippet" << EOF

    location ~ ^/ws/ {
        proxy_pass http://${upstream_addr}:5000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
        proxy_socket_keepalive on;
    }

    location / {
        proxy_pass http://${upstream_addr}:5000;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_read_timeout 86400s;
    }
}
EOF
        print_success "Nginx snippet: $out_dir/nginx.betterdesk.conf.snippet"
    else
        cat > "$out_dir/caddy.Caddyfile.snippet" << EOF
# BetterDesk reverse-proxy snippet (#267) — merge into /etc/caddy/Caddyfile
# Caddy obtains TLS automatically when this block is active.

${panel_host} {
EOF
        if [ "$route_wss" = "yes" ]; then
            cat >> "$out_dir/caddy.Caddyfile.snippet" << EOF
    handle /ws/id {
        reverse_proxy ${upstream_addr}:21118
    }
    handle /ws/relay {
        reverse_proxy ${upstream_addr}:21119
    }
EOF
        fi
        cat >> "$out_dir/caddy.Caddyfile.snippet" << EOF
    reverse_proxy ${upstream_addr}:5000

    encode gzip zstd
    header {
        X-Content-Type-Options nosniff
        X-Frame-Options DENY
        Referrer-Policy strict-origin-when-cross-origin
    }
}
EOF
        if [ "$panel_host" != "$server_id" ]; then
            cat >> "$out_dir/caddy.Caddyfile.snippet" << EOF

# Optional second site when ID/relay clients use a different hostname:
# ${server_id} {
#     handle /ws/id { reverse_proxy ${upstream_addr}:21118 }
#     handle /ws/relay { reverse_proxy ${upstream_addr}:21119 }
# }
EOF
        fi
        print_success "Caddy snippet: $out_dir/caddy.Caddyfile.snippet"
    fi

    cat > "$out_dir/firewall-notes.txt" << EOF
BetterDesk firewall notes — see docs/CONNECTION_PORTS.md for all profiles.

WAN (clients reach these directly — cannot be HTTP-proxied):
  - 21116/tcp + 21116/udp  Signal (registration, hole punching)
  - 21117/tcp              Relay data

Client API (needed for branding, enrollment, short-code pairing):
  - 21114/tcp              Go Client API — expose only over TLS, or via the proxy on 443
  - 21121/tcp              Legacy compat proxy (existing fleets only)

Through your reverse proxy (HTTPS :443):
  - Panel + console WebSockets -> http://${upstream_addr}:5000
$( [ "$route_wss" = "yes" ] && echo "  - RustDesk WSS /ws/id -> ${upstream_addr}:21118, /ws/relay -> ${upstream_addr}:21119" )
$( [ "$panel_bind" = "0.0.0.0" ] && echo "  - Panel bind: 0.0.0.0:5000 (remote proxy) — restrict :5000 to proxy IP in firewall" )

Localhost only (never expose):
  - 5000/tcp               Console panel
  - 5443/tcp               Console panel (native HTTPS mode)

Example (ufw), recommended profile with a reverse proxy:
  sudo ufw allow 443/tcp
  sudo ufw allow 21116/tcp
  sudo ufw allow 21116/udp
  sudo ufw allow 21117/tcp
  # Keep 21114 off WAN when the proxy reaches it on loopback:
  sudo ufw deny 21114/tcp

Verify the running profile:
  sudo bash scripts/check-connection-ports.sh b
EOF

    cat > "$out_dir/verify.sh" << 'VERIFYEOF'
#!/usr/bin/env bash
# BetterDesk reverse-proxy verification (#267)
set -euo pipefail
PANEL_HOST="${1:-}"
if [ -z "$PANEL_HOST" ]; then
    echo "Usage: $0 <public-hostname>"
    exit 1
fi
echo "=== Local panel (HTTP) ==="
curl -sI "http://127.0.0.1:5000/" | head -5 || true
echo ""
echo "=== Public panel (HTTPS via proxy) ==="
curl -sI "https://${PANEL_HOST}/" | head -5 || true
echo ""
echo "=== Console WebSocket upgrade ==="
curl -i -N --max-time 8 \
  -H "Connection: Upgrade" -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Version: 13" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  "https://${PANEL_HOST}/ws/bd-signal" 2>/dev/null | head -8 || true
VERIFYEOF
    chmod +x "$out_dir/verify.sh"
    # Inject hostname into verify script usage (already passed as arg)

    echo ""
    print_info "Reverse-proxy files written to: $out_dir"
    print_info "  betterdesk.env.snippet"
    [ "$proxy_type" = "nginx" ] && print_info "  nginx.betterdesk.conf.snippet" || print_info "  caddy.Caddyfile.snippet"
    print_info "  verify.sh $panel_host"
    print_info "  firewall-notes.txt"
    print_info "Documentation: docs/setup/REVERSE_PROXY.md"
    echo ""
    print_warning "Configure your proxy, then open https://${panel_host}/ (not :5443)"
    if [ "$panel_host" != "$server_id" ]; then
        print_info "RustDesk clients: ID server ${server_id} (set PUBLIC_SERVER_ID in .env)"
    fi

    REVERSE_PROXY_GENERATED_HOST="$panel_host"
    REVERSE_PROXY_GENERATED_SERVER_ID="$server_id"
    REVERSE_PROXY_GENERATED_WS_ORIGINS="$ws_origins"
    REVERSE_PROXY_PANEL_BIND="$panel_bind"
    REVERSE_PROXY_UPSTREAM_ADDR="$upstream_addr"
}

# Interactive reverse-proxy wizard: apply BetterDesk settings + emit proxy snippets (#267).
do_configure_reverse_proxy() {
    local panel_host server_id ws_origins panel_bind

    echo ""
    print_step "Configuring BetterDesk for external reverse proxy (TLS at Caddy/Nginx)..."
    print_info "Your proxy terminates TLS on :443; BetterDesk panel stays plain HTTP"
    echo ""

    read -p "Public panel hostname (e.g., console.example.com): " panel_host
    if [ -z "$panel_host" ]; then
        print_error "Hostname is required"
        return 1
    fi

    if ! generate_reverse_proxy_config "$panel_host" "prompt" "prompt" ""; then
        return 1
    fi

    server_id="${REVERSE_PROXY_GENERATED_SERVER_ID:-$panel_host}"
    ws_origins="${REVERSE_PROXY_GENERATED_WS_ORIGINS:-https://${panel_host}}"
    panel_bind="${REVERSE_PROXY_PANEL_BIND:-127.0.0.1}"

    apply_console_reverse_proxy_mode "$panel_host" "$server_id" "$ws_origins" "$panel_bind"

    print_success "BetterDesk configured for external reverse proxy"
    echo ""
    if [ "$panel_bind" = "0.0.0.0" ]; then
        print_info "  Panel (bind):   http://0.0.0.0:$(resolve_panel_http_port) (remote proxy host)"
        print_info "  Proxy upstream: http://${REVERSE_PROXY_UPSTREAM_ADDR:-<lan-ip>}:$(resolve_panel_http_port)"
    else
        print_info "  Panel (local):  http://127.0.0.1:$(resolve_panel_http_port)"
    fi
    print_info "  Panel (public): https://${panel_host}/"
    print_info "  TRUST_PROXY:    Y (console + Go server)"
    if [ "$panel_bind" = "0.0.0.0" ]; then
        print_info "  TRUSTED_PROXIES: set to your reverse-proxy IP/CIDR in .env (required for Go WSS)"
    else
        print_info "  TRUSTED_PROXIES: 127.0.0.1/32,::1/128 (same-host)"
    fi
    print_info "  Signal/Relay:   TCP :21116 / :21117 (direct — not HTTP-proxied)"
    echo ""
    print_info "Copy proxy snippet from $RUSTDESK_PATH/reverse-proxy/ into Caddy/Nginx, then reload the proxy."
}

# The console's HTTP port (the redirect when HTTPS is on): CONSOLE_PORT for
# the server with its console, PORT for the Node.js console; default 5000.
resolve_panel_http_port() {
    local port=""
    is_go_only_install && port=$(read_effective_console_setting CONSOLE_PORT "")
    [ -n "$port" ] || port=$(read_effective_console_setting PORT 5000)
    echo "$port"
}

# HTTPS panel listen port (HTTPS_PORT, default 5443 — never conflated with PORT).
resolve_panel_https_port() {
    read_effective_console_setting HTTPS_PORT 5443
}

# Primary panel port for health checks: HTTPS_PORT when HTTPS enabled, else PORT.
resolve_panel_health_port() {
    local https_enabled
    https_enabled=$(read_effective_console_setting HTTPS_ENABLED false)
    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" = "true" ]; then
        resolve_panel_https_port
    else
        resolve_panel_http_port
    fi
}

# Offer native HTTPS on standard port 443 after enabling TLS (#219 follow-up).
maybe_offer_standard_https_port() {
    local env_file="${CONSOLE_PATH}/.env"
    local svc_file="/etc/systemd/system/betterdesk-console.service"
    local current_https_port

    current_https_port=$(read_effective_console_setting HTTPS_PORT 5443)
    [ "$current_https_port" = "443" ] && return 0

    echo ""
    print_info "Panel HTTPS defaults to port 5443 (avoids conflicts with nginx/certbot on :443)."
    if confirm "Use standard HTTPS port 443 (https://your-domain without :5443)?"; then
        _upsert_env_line "$env_file" HTTPS_PORT 443
        set_panel_http_port 80
        _upsert_env_line "$env_file" HTTP_REDIRECT_HTTPS true
        if is_go_only_install; then
            # Ports below 1024 need CAP_NET_BIND_SERVICE in the unit
            maybe_update_services >/dev/null
        elif [ -f "$svc_file" ]; then
            _upsert_systemd_env "$svc_file" HTTPS_PORT 443
            _upsert_systemd_env "$svc_file" PORT 80
            _upsert_systemd_env "$svc_file" HTTP_REDIRECT_HTTPS true
            systemctl daemon-reload 2>/dev/null || true
        fi
        ensure_service_user >/dev/null
        print_success "Standard ports configured: HTTPS :443, HTTP redirect :80"
        print_info "Ensure nothing else listens on :443/:80; open firewall: ufw allow 443/tcp (and 80/tcp if redirecting)"
    fi
}

# True when Client API (:21121) should be probed over HTTPS (RUSTDESK_API_TLS + cert present).
client_api_should_use_tls() {
    local mode cert_path key_path
    mode=$(read_effective_console_setting RUSTDESK_API_TLS auto)
    mode=$(echo "$mode" | tr '[:upper:]' '[:lower:]')
    if [ "$mode" = "false" ] || [ "$mode" = "0" ] || [ "$mode" = "off" ] || [ "$mode" = "http" ]; then
        return 1
    fi
    cert_path=$(read_effective_console_setting SSL_CERT_PATH "")
    key_path=$(read_effective_console_setting SSL_KEY_PATH "")
    if [ -z "$cert_path" ] || [ ! -f "$cert_path" ] || [ -z "$key_path" ] || [ ! -f "$key_path" ]; then
        return 1
    fi
    if [ "$mode" = "true" ] || [ "$mode" = "1" ] || [ "$mode" = "on" ] || [ "$mode" = "https" ]; then
        return 0
    fi
    # auto — certs present
    return 0
}

# Before a start: the server's files belong to the service account and the
# TLS files it needs are readable.
prepare_console_after_update() {
    is_go_only_install || return 0
    systemctl reset-failed "$SERVER_SERVICE" 2>/dev/null || true
    repair_https_stuck_state yes || true
    set_server_folder_permissions
    ensure_console_tls_material_readable 2>/dev/null || true
    return 0
}

# Start betterdesk-server and check that it answers (#306).
start_services_with_verification() {
    print_step "Starting betterdesk-server with health verification..."

    local has_errors=false
    if ! systemctl is-active --quiet "$SERVER_SERVICE" 2>/dev/null; then
        if ! check_port_available "21116" "signal"; then
            print_error "Port 21116 (ID server) is not available"
            has_errors=true
        fi
        if ! check_port_available "21117" "relay"; then
            print_error "Port 21117 (relay) is not available"
            has_errors=true
        fi
    fi
    if [ "$has_errors" = true ]; then
        print_error "Cannot start betterdesk-server - ports are in use"
        print_info "Try: sudo lsof -i :21116 and sudo lsof -i :21117 to find conflicts"
        return 1
    fi

    prepare_console_after_update || print_warning "Preparing the start reported issues (continuing)"
    systemctl enable "$SERVER_SERVICE" 2>/dev/null || true
    if systemctl is-active --quiet "$SERVER_SERVICE" 2>/dev/null; then
        systemctl restart "$SERVER_SERVICE" || true
    else
        systemctl start "$SERVER_SERVICE" || true
    fi

    if ! verify_go_install 90; then
        print_error "betterdesk-server did not come up"
        print_info "Run: journalctl -u betterdesk-server -n 50 --no-pager"
        return 1
    fi
    print_success "betterdesk-server started and verified"
    return 0
}

#===============================================================================
# Detection Functions
#===============================================================================

detect_installation() {
    INSTALL_STATUS="none"
    HBBS_RUNNING=false
    HBBR_RUNNING=false
    CONSOLE_RUNNING=false
    BINARIES_OK=false
    DATABASE_OK=false
    CONSOLE_TYPE="none"
    
    # Check paths
    if [ -d "$RUSTDESK_PATH" ]; then
        INSTALL_STATUS="partial"
        
        # Check Go server binary (primary) or legacy Rust binaries
        if [ -f "$RUSTDESK_PATH/betterdesk-server" ]; then
            BINARIES_OK=true
            SERVER_TYPE="go"
        elif [ -f "$RUSTDESK_PATH/hbbs" ] || [ -f "$RUSTDESK_PATH/hbbs-v8-api" ]; then
            BINARIES_OK=true
            SERVER_TYPE="rust"
            print_warning "Legacy Rust binaries detected. Consider upgrading to Go server."
        fi
        
        # Check database (SQLite file or PostgreSQL connection)
        local detected_db_type="sqlite"
        if [ -f "$CONSOLE_PATH/.env" ]; then
            detected_db_type=$(grep -m1 '^DB_TYPE=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')
            detected_db_type="${detected_db_type:-sqlite}"
        fi

        if [ "$detected_db_type" = "postgres" ]; then
            # PostgreSQL: check via systemd service config or .env
            local pg_uri
            pg_uri=$(grep -m1 '^DATABASE_URL=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2-)
            if [ -n "$pg_uri" ]; then
                if PGCONNECT_TIMEOUT=3 psql "$pg_uri" -c "SELECT 1" &>/dev/null 2>&1; then
                    DATABASE_OK=true
                fi
            fi
        else
            # SQLite: check file exists
            if [ -f "$DB_PATH" ]; then
                DATABASE_OK=true
            fi
        fi
    fi
    
    # Detect console type: the console inside the server, or the old Node.js one
    if [ -d "$CONSOLE_PATH" ]; then
        if is_go_only_install && [ -f "$CONSOLE_PATH/.env" ]; then
            CONSOLE_TYPE="go"
        elif [ -f "$CONSOLE_PATH/server.js" ] || [ -f "$CONSOLE_PATH/package.json" ]; then
            CONSOLE_TYPE="nodejs"
        fi

        if [ "$CONSOLE_TYPE" != "none" ] && [ "$BINARIES_OK" = true ] && [ "$DATABASE_OK" = true ]; then
            INSTALL_STATUS="complete"
        fi
    fi
    
    # Check services (Go server or legacy Rust)
    if systemctl is-active --quiet betterdesk-server 2>/dev/null; then
        HBBS_RUNNING=true
        HBBR_RUNNING=true  # Go server handles both
    elif systemctl is-active --quiet rustdesksignal 2>/dev/null || \
         systemctl is-active --quiet hbbs 2>/dev/null; then
        HBBS_RUNNING=true
    fi
    
    if ! [ "$HBBR_RUNNING" = true ]; then
        if systemctl is-active --quiet rustdeskrelay 2>/dev/null || \
           systemctl is-active --quiet hbbr 2>/dev/null; then
            HBBR_RUNNING=true
        fi
    fi
    
    if [ "$CONSOLE_TYPE" = "go" ]; then
        if [ "$HBBS_RUNNING" = true ] && [ "$(read_effective_console_setting CONSOLE_PORT 5000)" != "0" ]; then
            CONSOLE_RUNNING=true
        fi
    elif systemctl is-active --quiet "$LEGACY_CONSOLE_SERVICE" 2>/dev/null; then
        CONSOLE_RUNNING=true
    fi
}

# Preserve database configuration from existing .env file
# This MUST be called before the .env is written during UPDATE/REPAIR
# to prevent switching from PostgreSQL to SQLite
preserve_database_config() {
    if [ -f "$CONSOLE_PATH/.env" ]; then
        local existing_db_type existing_db_url
        existing_db_type=$(grep -m1 '^DB_TYPE=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')
        existing_db_url=$(grep -m1 '^DATABASE_URL=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2-)
        
        if [ "$existing_db_type" = "postgres" ] && [ -n "$existing_db_url" ]; then
            USE_POSTGRESQL="true"
            POSTGRESQL_URI="$existing_db_url"
            print_info "Preserving PostgreSQL configuration from existing .env"
        elif [ "$existing_db_type" = "sqlite" ]; then
            USE_POSTGRESQL="false"
            POSTGRESQL_URI=""
            print_info "Preserving SQLite configuration from existing .env"
        fi
    fi
}

# Write or merge the console's .env from the template built into the server
# (issue #158), then let upgrade-env add what the server needs (CONSOLE_PORT,
# BETTERDESK_ENV_FILE, the pinned ports) and comment out what it does not.
# Usage: merge_console_env true   — fresh install (full template)
#        merge_console_env false  — update (append missing keys only)
merge_console_env() {
    local fresh_install="${1:-false}"
    local ssl_dir="$RUSTDESK_PATH/ssl"
    local env_file="$CONSOLE_PATH/.env"
    local db_type="sqlite"
    local database_url=""
    local admin_password="${ADMIN_PASSWORD:-}"
    local session_secret=""
    local remote_target_vault_key=""
    local go_port="${GO_API_PORT:-21114}"
    local client_port="${CLIENT_API_PORT:-21121}"
    local bin
    bin=$(server_binary_path)

    if ! binary_has_subcommands "$bin"; then
        print_error "$bin is missing or too old to write .env"
        return 1
    fi

    if [ "$USE_POSTGRESQL" = "true" ] && [ -n "$POSTGRESQL_URI" ]; then
        db_type="postgres"
        database_url="$POSTGRESQL_URI"
    fi

    if [ "$fresh_install" = "true" ]; then
        if [ -z "$admin_password" ]; then
            admin_password=$(openssl rand -hex 16)
        fi
        session_secret=$(openssl rand -hex 32)
    else
        session_secret=$(env_file_value "$env_file" SESSION_SECRET)
        if [ -z "$admin_password" ]; then
            admin_password=$(env_file_value "$env_file" DEFAULT_ADMIN_PASSWORD)
        fi
        if [ -z "$session_secret" ]; then
            session_secret=$(openssl rand -hex 32)
        fi
    fi
    remote_target_vault_key=$(env_file_value "$env_file" REMOTE_TARGET_VAULT_KEY)
    if [ -z "$remote_target_vault_key" ]; then
        remote_target_vault_key=$(openssl rand -hex 32)
    fi

    local -a merge_args=(env merge --target "$env_file" --subst-env)
    [ "$fresh_install" = "true" ] && merge_args+=(--fresh)

    local merge_output=""
    if ! merge_output=$(BETTERDESK_ENV_FILE='' \
        BD_SUBST_RUSTDESK_DIR="$RUSTDESK_PATH" \
        BD_SUBST_PUB_KEY_PATH="$RUSTDESK_PATH/id_ed25519.pub" \
        BD_SUBST_API_KEY_PATH="$RUSTDESK_PATH/.api_key" \
        BD_SUBST_DB_TYPE="$db_type" \
        BD_SUBST_DB_PATH="$RUSTDESK_PATH/db_v2.sqlite3" \
        BD_SUBST_DATABASE_URL="$database_url" \
        BD_SUBST_DATA_DIR="$CONSOLE_PATH/data" \
        BD_SUBST_GO_API_PORT="$go_port" \
        BD_SUBST_HBBS_API_URL="http://localhost:${go_port}/api" \
        BD_SUBST_BETTERDESK_API_URL="http://localhost:${go_port}/api" \
        BD_SUBST_API_PORT="$client_port" \
        BD_SUBST_DEFAULT_ADMIN_PASSWORD="$admin_password" \
        BD_SUBST_SESSION_SECRET="$session_secret" \
        BD_SUBST_REMOTE_TARGET_VAULT_KEY="$remote_target_vault_key" \
        BD_SUBST_GUACD_ADDRESS="127.0.0.1:4822" \
        BD_SUBST_SSL_CERT_PATH="$ssl_dir/betterdesk.crt" \
        BD_SUBST_SSL_KEY_PATH="$ssl_dir/betterdesk.key" \
        "$bin" "${merge_args[@]}" 2>&1) || ! printf '%s' "$merge_output" | grep -q '"success":true'; then
        print_error "Failed to write .env (betterdesk-server env merge)"
        [ -n "$merge_output" ] && print_info "$merge_output"
        return 1
    fi

    # The server's database: DB_URL (the old units passed it as -db)
    if ! grep -q '^DB_URL=' "$env_file"; then
        if [ "$db_type" = "postgres" ]; then
            printf 'DB_URL=%s\n' "$database_url" >> "$env_file"
        else
            printf 'DB_URL=%s\n' "$RUSTDESK_PATH/db_v2.sqlite3" >> "$env_file"
        fi
    fi
    # The server makes the first admin before the console starts, and the
    # console keeps an admin it did not make: both take the same password
    if [ "$fresh_install" = "true" ] && ! grep -q '^INIT_ADMIN_PASS=' "$env_file"; then
        printf 'INIT_ADMIN_PASS=%s\n' "$admin_password" >> "$env_file"
    fi
    # A new install starts in Managed: new clients wait for approval. This
    # is only the first default; a mode saved in the console wins.
    if [ "$fresh_install" = "true" ] && ! grep -q '^ENROLLMENT_MODE=' "$env_file"; then
        printf 'ENROLLMENT_MODE=managed\n' >> "$env_file"
    fi
    # The update channel the installer was given, over the template's main
    save_given_update_channel

    if ! BETTERDESK_ENV_FILE='' "$bin" upgrade-env --from "$env_file" --write --supervisor systemd >/dev/null; then
        print_error "betterdesk-server upgrade-env could not finish .env"
        return 1
    fi
    # A fresh file has no older version to keep
    [ "$fresh_install" = "true" ] && rm -f "$env_file.pre-go"

    chmod 600 "$env_file" 2>/dev/null || true
    if [ "$fresh_install" = "true" ]; then
        print_info "Created .env configuration file (fresh install)"
    else
        print_info "Merged new .env keys (existing settings preserved)"
    fi
    return 0
}

detect_architecture() {
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64) ARCH_NAME="x86_64" ;;
        aarch64|arm64) ARCH_NAME="aarch64" ;;
        armv7l) ARCH_NAME="armv7" ;;
        *) ARCH_NAME="unknown" ;;
    esac
}

detect_os() {
    if [ -f /etc/os-release ]; then
        OS_NAME=$(grep -m1 '^NAME=' /etc/os-release | cut -d= -f2- | sed 's/^"//; s/"$//' || echo "Unknown")
        OS_VERSION=$(grep -m1 '^VERSION_ID=' /etc/os-release | cut -d= -f2- | sed 's/^"//; s/"$//' || echo "")
    else
        OS_NAME="Unknown"
        OS_VERSION=""
    fi
}

# Auto-detect RustDesk installation path
auto_detect_paths() {
    local found=false
    
    # If RUSTDESK_PATH is already set (via env var), validate it
    if [ -n "$RUSTDESK_PATH" ]; then
        if [ -d "$RUSTDESK_PATH" ] && { [ -f "$RUSTDESK_PATH/betterdesk-server" ] || [ -f "$RUSTDESK_PATH/hbbs" ] || [ -f "$RUSTDESK_PATH/hbbs-v8-api" ]; }; then
            print_info "Using configured RustDesk path: $RUSTDESK_PATH"
        else
            print_warning "Configured RUSTDESK_PATH ($RUSTDESK_PATH) is invalid"
            RUSTDESK_PATH=""
        fi
    fi
    
    # Auto-detect if not found
    if [ -z "$RUSTDESK_PATH" ]; then
        for path in "${COMMON_RUSTDESK_PATHS[@]}"; do
            if [ -d "$path" ] && { [ -f "$path/betterdesk-server" ] || [ -f "$path/hbbs" ] || [ -f "$path/hbbs-v8-api" ]; }; then
                RUSTDESK_PATH="$path"
                print_success "Detected RustDesk installation: $RUSTDESK_PATH"
                break
            fi
        done
    fi
    
    # If still not found, use default for new installations
    if [ -z "$RUSTDESK_PATH" ]; then
        RUSTDESK_PATH="/opt/betterdesk"
        print_info "No installation detected. Default path: $RUSTDESK_PATH"
    fi
    
    # Auto-detect the console's folder (its .env and data) and type
    CONSOLE_TYPE="none"

    if [ -n "$CONSOLE_PATH" ]; then
        if [ -d "$CONSOLE_PATH" ] && { [ -f "$CONSOLE_PATH/.env" ] || [ -f "$CONSOLE_PATH/server.js" ]; }; then
            print_info "Using configured console path: $CONSOLE_PATH"
        else
            print_warning "Configured CONSOLE_PATH ($CONSOLE_PATH) is invalid"
            CONSOLE_PATH=""
        fi
    fi

    # The server's unit names the console's env file
    if [ -z "$CONSOLE_PATH" ] && is_go_only_install; then
        local env_from_unit
        env_from_unit=$(unit_env_value "$SERVER_UNIT" BETTERDESK_ENV_FILE)
        if [ -n "$env_from_unit" ] && [ -f "$env_from_unit" ]; then
            CONSOLE_PATH=$(dirname "$env_from_unit")
            print_success "Detected the console folder: $CONSOLE_PATH"
        fi
    fi

    if [ -z "$CONSOLE_PATH" ]; then
        for path in "${COMMON_CONSOLE_PATHS[@]}"; do
            if [ -d "$path" ] && { [ -f "$path/.env" ] || [ -f "$path/server.js" ]; }; then
                CONSOLE_PATH="$path"
                print_success "Detected the console folder: $CONSOLE_PATH"
                break
            fi
        done
    fi
    if is_go_only_install; then
        CONSOLE_TYPE="go"
    elif [ -n "$CONSOLE_PATH" ] && [ -f "$CONSOLE_PATH/server.js" ]; then
        CONSOLE_TYPE="nodejs"
    fi
    
    # Default Console path if not found
    if [ -z "$CONSOLE_PATH" ]; then
        CONSOLE_PATH="/opt/BetterDeskConsole"
    fi
    
    # Update DB_PATH based on detected RUSTDESK_PATH
    DB_PATH="$RUSTDESK_PATH/db_v2.sqlite3"
    
    return 0
}

# Interactive path configuration
configure_paths() {
    local _menu_items=(
        $'Auto-detect paths\tScan common install locations'
        $'Set server path\tManually set the RustDesk server path'
        $'Set console path\tManually set the web console path'
        $'Reset to defaults\t/opt/betterdesk + /opt/BetterDeskConsole'
        $'Back\tReturn to the main menu'
    )
    local _menu_returns=( 1 2 3 4 0 )
    local subtitle="server: ${RUSTDESK_PATH:-unset} | console: ${CONSOLE_PATH:-unset}"
    menu_choose "Path Configuration" "$subtitle"
    local choice="$MENU_CHOICE"

    case $choice in
        1)
            RUSTDESK_PATH=""
            CONSOLE_PATH=""
            auto_detect_paths
            press_enter
            configure_paths
            ;;
        2)
            echo ""
            echo -n "Enter RustDesk server path (e.g., /opt/rustdesk): "
            read -r new_path
            if [ -n "$new_path" ]; then
                if [ -d "$new_path" ]; then
                    RUSTDESK_PATH="$new_path"
                    DB_PATH="$RUSTDESK_PATH/db_v2.sqlite3"
                    print_success "RustDesk path set to: $RUSTDESK_PATH"
                else
                    print_warning "Directory does not exist: $new_path"
                    if confirm "Create this directory?"; then
                        mkdir -p "$new_path"
                        RUSTDESK_PATH="$new_path"
                        DB_PATH="$RUSTDESK_PATH/db_v2.sqlite3"
                        print_success "Created and set RustDesk path: $RUSTDESK_PATH"
                    fi
                fi
            fi
            press_enter
            configure_paths
            ;;
        3)
            echo ""
            echo -n "Enter Console path (e.g., /opt/BetterDeskConsole): "
            read -r new_path
            if [ -n "$new_path" ]; then
                if [ -d "$new_path" ]; then
                    CONSOLE_PATH="$new_path"
                    print_success "Console path set to: $CONSOLE_PATH"
                else
                    print_warning "Directory does not exist: $new_path"
                    if confirm "Create this directory?"; then
                        mkdir -p "$new_path"
                        CONSOLE_PATH="$new_path"
                        print_success "Created and set Console path: $CONSOLE_PATH"
                    fi
                fi
            fi
            press_enter
            configure_paths
            ;;
        4)
            RUSTDESK_PATH="/opt/betterdesk"
            CONSOLE_PATH="/opt/BetterDeskConsole"
            DB_PATH="$RUSTDESK_PATH/db_v2.sqlite3"
            print_success "Paths reset to defaults"
            press_enter
            configure_paths
            ;;
        0|"")
            return
            ;;
        *)
            print_error "Invalid option"
            press_enter
            configure_paths
            ;;
    esac
}

print_status() {
    detect_installation
    detect_architecture
    detect_os
    
    echo ""
    echo -e "${WHITE}${BOLD}═══ System Status ═══${NC}"
    echo ""
    echo -e "  System:       ${CYAN}$OS_NAME $OS_VERSION${NC}"
    echo -e "  Architecture: ${CYAN}$ARCH_NAME${NC}"
    echo ""
    
    echo -e "${WHITE}${BOLD}═══ Configured Paths ═══${NC}"
    echo ""
    echo -e "  RustDesk:     ${CYAN}$RUSTDESK_PATH${NC}"
    echo -e "  Console:      ${CYAN}$CONSOLE_PATH${NC}"
    
    # Show database type and path/URI
    local diag_db_type="sqlite"
    if [ -f "$CONSOLE_PATH/.env" ]; then
        diag_db_type=$(grep -m1 '^DB_TYPE=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')
        diag_db_type="${diag_db_type:-sqlite}"
    fi
    if [ "$diag_db_type" = "postgres" ]; then
        local diag_pg_uri
        diag_pg_uri=$(grep -m1 '^DATABASE_URL=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2-)
        # Mask password in URI for display
        local diag_pg_display
        diag_pg_display=$(echo "$diag_pg_uri" | sed 's|://[^:]*:[^@]*@|://***:***@|')
        echo -e "  Database:     ${CYAN}PostgreSQL${NC} ($diag_pg_display)"
    else
        echo -e "  Database:     ${CYAN}SQLite${NC} ($DB_PATH)"
    fi
    echo ""
    
    echo -e "${WHITE}${BOLD}═══ Installation Status ═══${NC}"
    echo ""
    
    # Installation status
    case "$INSTALL_STATUS" in
        "complete")
            echo -e "  Status:       ${GREEN}✓ Installed${NC}"
            ;;
        "partial")
            echo -e "  Status:       ${YELLOW}! Partial installation${NC}"
            ;;
        "none")
            echo -e "  Status:       ${RED}✗ Not installed${NC}"
            ;;
    esac
    
    # Components
    if [ "$BINARIES_OK" = true ]; then
        echo -e "  Binaries:      ${GREEN}✓ OK${NC}"
    else
        echo -e "  Binaries:      ${RED}✗ Not found${NC}"
    fi
    
    if [ "$DATABASE_OK" = true ]; then
        echo -e "  Database:  ${GREEN}✓ OK${NC}"
    else
        echo -e "  Database:  ${RED}✗ Not found${NC}"
    fi
    
    if [ -d "$CONSOLE_PATH" ]; then
        case "$CONSOLE_TYPE" in
            go) echo -e "  Web Console:  ${GREEN}✓ OK${NC} (inside betterdesk-server)" ;;
            nodejs) echo -e "  Web Console:  ${YELLOW}! Node.js${NC} (Update moves it into betterdesk-server)" ;;
            *) echo -e "  Web Console:  ${GREEN}✓ OK${NC}" ;;
        esac
    else
        echo -e "  Web Console:  ${RED}✗ Not found${NC}"
    fi
    
    echo ""
    echo -e "${WHITE}${BOLD}═══ Services Status ═══${NC}"
    echo ""
    
    # Check if using Go server (single binary) or legacy Rust (two binaries)
    if [ "${SERVER_TYPE:-}" = "go" ] || systemctl is-active --quiet betterdesk-server 2>/dev/null; then
        local go_state
        go_state=$(systemctl show betterdesk-server --property=ActiveState --value 2>/dev/null || echo "unknown")
        case "$go_state" in
            active)
                echo -e "  BetterDesk Server: ${GREEN}● Active${NC} (Signal + Relay + API$([ "$CONSOLE_TYPE" = go ] && [ "$CONSOLE_RUNNING" = true ] && echo " + web console"))"
                ;;
            failed)
                echo -e "  BetterDesk Server: ${RED}✗ Failed${NC} (check: journalctl -u betterdesk-server -n 30)"
                ;;
            activating)
                echo -e "  BetterDesk Server: ${YELLOW}◌ Starting...${NC}"
                ;;
            *)
                echo -e "  BetterDesk Server: ${RED}○ Inactive${NC} ($go_state)"
                ;;
        esac
    else
        # Legacy Rust servers
        if [ "$HBBS_RUNNING" = true ]; then
            echo -e "  HBBS (Signal): ${GREEN}● Active${NC} ${YELLOW}(Legacy Rust)${NC}"
        else
            echo -e "  HBBS (Signal): ${RED}○ Inactive${NC}"
        fi
        
        if [ "$HBBR_RUNNING" = true ]; then
            echo -e "  HBBR (Relay):  ${GREEN}● Active${NC} ${YELLOW}(Legacy Rust)${NC}"
        else
            echo -e "  HBBR (Relay):  ${RED}○ Inactive${NC}"
        fi
    fi
    
    # The old Node.js console, while an install still has it
    if [ "$CONSOLE_TYPE" = "nodejs" ]; then
        local console_state
        console_state=$(systemctl show "$LEGACY_CONSOLE_SERVICE" --property=ActiveState --value 2>/dev/null || echo "unknown")
        case "$console_state" in
            active) echo -e "  Web Console (Node.js): ${GREEN}● Active${NC}" ;;
            failed) echo -e "  Web Console (Node.js): ${RED}✗ Failed${NC} (check: journalctl -u betterdesk-server -n 30)" ;;
            *) echo -e "  Web Console (Node.js): ${RED}○ Inactive${NC} ($console_state)" ;;
        esac
    fi
    
    echo ""
}

#===============================================================================
# Go Installation and Compilation
#===============================================================================

# Extract numeric semver component (handles "1.25+", "26rc1", etc.).
_go_version_part() {
    local ver="$1" field="${2:-1}"
    local part
    part=$(echo "$ver" | cut -d'.' -f"$field" | grep -oE '^[0-9]+' | head -1)
    echo "${part:-0}"
}

check_go_installed() {
    if command -v go &> /dev/null; then
        local go_version
        go_version=$(go version | awk '{print $3}' | sed 's/go//')
        local go_major=$(_go_version_part "$go_version" 1)
        local go_minor=$(_go_version_part "$go_version" 2)
        local go_patch=$(_go_version_part "$go_version" 3)
        local min_major=$(_go_version_part "$GO_MIN_VERSION" 1)
        local min_minor=$(_go_version_part "$GO_MIN_VERSION" 2)
        local min_patch=$(_go_version_part "$GO_MIN_VERSION" 3)

        # Security hardening: reject vulnerable Go 1.26.0 stdlib.
        if [ "$go_major" -eq 1 ] && [ "$go_minor" -eq 26 ] && [ "$go_patch" -eq 0 ]; then
            print_warning "Detected vulnerable Go version $go_version (known stdlib CVEs)."
            return 1
        fi
        
        if [ "$go_major" -gt "$min_major" ] ||
           ([ "$go_major" -eq "$min_major" ] && [ "$go_minor" -gt "$min_minor" ]) ||
           ([ "$go_major" -eq "$min_major" ] && [ "$go_minor" -eq "$min_minor" ] && [ "$go_patch" -ge "$min_patch" ]); then
            return 0
        fi
    fi
    return 1
}

install_golang() {
    print_step "Installing Go $GO_MIN_VERSION+..."
    
    # Ensure architecture is detected
    if [ -z "$ARCH_NAME" ]; then
        detect_architecture
    fi
    
    if check_go_installed; then
        local go_version=$(go version | awk '{print $3}' | sed 's/go//')
        print_info "Go $go_version is already installed"
        return 0
    fi
    
    local go_version="$GO_DOWNLOAD_VERSION"
    local go_arch=""
    
    case "$ARCH_NAME" in
        x86_64) go_arch="amd64" ;;
        aarch64) go_arch="arm64" ;;
        armv7*) go_arch="armv6l" ;;
        *) print_error "Unsupported architecture: $ARCH_NAME"; return 1 ;;
    esac
    
    local go_tarball="go${go_version}.linux-${go_arch}.tar.gz"
    local go_url="https://go.dev/dl/$go_tarball"
    
    print_info "Downloading Go $go_version for $go_arch..."
    
    cd /tmp
    if command -v wget &> /dev/null; then
        wget -q --show-progress "$go_url" -O "$go_tarball" || wget "$go_url" -O "$go_tarball"
    elif command -v curl &> /dev/null; then
        curl -fSL --progress-bar "$go_url" -o "$go_tarball"
    else
        print_error "Neither wget nor curl available"
        return 1
    fi
    
    print_info "Installing Go to /usr/local/go..."
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "$go_tarball"
    rm "$go_tarball"
    
    # Add to PATH for current session
    export PATH=$PATH:/usr/local/go/bin
    
    # Add to system-wide PATH
    if [ ! -f /etc/profile.d/go.sh ]; then
        echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
        chmod +x /etc/profile.d/go.sh
    fi
    
    if check_go_installed; then
        print_success "Go $go_version installed successfully"
        return 0
    else
        print_error "Go installation failed"
        return 1
    fi
}

# Probe a fixed HTTPS host without executing response body (connectivity only).
# Optional 3rd arg: curl IP family flag ("-4" or "-6"); empty = dual-stack default.
_go_probe_https() {
    local url="$1"
    local limit="${2:-$GO_MODULE_PREFLIGHT_TIMEOUT}"
    local ip_flag="${3:-}"

    if command -v curl &> /dev/null; then
        # No -f: any HTTP response means the TCP/TLS path works (404 is fine).
        # shellcheck disable=SC2086
        curl -sS -o /dev/null --connect-timeout "$limit" --max-time "$limit" $ip_flag "$url"
        return $?
    fi
    if command -v wget &> /dev/null; then
        # wget has no reliable -4/-6 on all distros; dual-stack only.
        wget -q --spider --timeout="$limit" --tries=1 "$url"
        return $?
    fi
    return 2
}

# Read a sysctl value or echo "unknown".
_go_sysctl_get() {
    local key="$1"
    if command -v sysctl &> /dev/null; then
        sysctl -n "$key" 2>/dev/null || echo "unknown"
    else
        echo "unknown"
    fi
}

# Temporarily disable IPv6 for Go dual-stack dials on broken-IPv6 VMs (save/restore).
_go_ipv4_force_begin() {
    GO_IPV4_SAVED_ALL=""
    GO_IPV4_SAVED_DEFAULT=""
    GO_IPV4_APPLIED=0

    if [ "${GO_FORCE_IPV4:-0}" != "1" ]; then
        return 0
    fi
    if ! command -v sysctl &> /dev/null; then
        print_warning "Broken IPv6 detected but sysctl unavailable; cannot force IPv4 for Go"
        return 0
    fi

    GO_IPV4_SAVED_ALL=$(_go_sysctl_get net.ipv6.conf.all.disable_ipv6)
    GO_IPV4_SAVED_DEFAULT=$(_go_sysctl_get net.ipv6.conf.default.disable_ipv6)

    if sysctl -w net.ipv6.conf.all.disable_ipv6=1 >/dev/null 2>&1 \
        && sysctl -w net.ipv6.conf.default.disable_ipv6=1 >/dev/null 2>&1; then
        GO_IPV4_APPLIED=1
        print_info "Temporarily disabled IPv6 for Go module download (broken IPv6 path detected)"
    else
        print_warning "Could not disable IPv6 via sysctl; Go may still hang on AAAA dials"
    fi
}

_go_ipv4_force_end() {
    if [ "${GO_IPV4_APPLIED:-0}" != "1" ]; then
        return 0
    fi
    if [ -n "${GO_IPV4_SAVED_ALL}" ] && [ "${GO_IPV4_SAVED_ALL}" != "unknown" ]; then
        sysctl -w "net.ipv6.conf.all.disable_ipv6=${GO_IPV4_SAVED_ALL}" >/dev/null 2>&1 || true
    fi
    if [ -n "${GO_IPV4_SAVED_DEFAULT}" ] && [ "${GO_IPV4_SAVED_DEFAULT}" != "unknown" ]; then
        sysctl -w "net.ipv6.conf.default.disable_ipv6=${GO_IPV4_SAVED_DEFAULT}" >/dev/null 2>&1 || true
    fi
    GO_IPV4_APPLIED=0
    print_info "Restored previous IPv6 sysctl settings"
}

# Drop leftover partial/lock files from a previous Ctrl+C during go mod download.
_go_clear_stale_module_partials() {
    local modcache
    modcache=$(go env GOMODCACHE 2>/dev/null || true)
    if [ -z "$modcache" ] || [ ! -d "$modcache" ]; then
        return 0
    fi
    local cleared=0
    cleared=$(find "$modcache" \( -name '*.partial' -o -name '*.lock' \) 2>/dev/null | wc -l | tr -d ' ')
    if [ "${cleared:-0}" -gt 0 ] 2>/dev/null; then
        print_warning "Removing ${cleared} stale Go module cache lock/partial file(s) from a prior interrupted download"
        find "$modcache" \( -name '*.partial' -o -name '*.lock' \) -delete 2>/dev/null || true
    fi
}

# Fail fast when proxy.golang.org / sum.golang.org are unreachable.
# Prefer IPv4 probes; set GO_FORCE_IPV4=1 when IPv6 is broken but IPv4 works.
_go_module_network_preflight() {
    local limit="${GO_MODULE_PREFLIGHT_TIMEOUT}"
    local probe_status=0
    local ipv4_ok=0
    local ipv6_ok=0

    GO_FORCE_IPV4=0
    print_info "Checking HTTPS reachability of Go module endpoints (${limit}s)..."

    if ! command -v curl &> /dev/null && ! command -v wget &> /dev/null; then
        print_warning "Neither curl nor wget available; skipping Go module network preflight"
        return 0
    fi

    if command -v curl &> /dev/null; then
        if _go_probe_https "https://proxy.golang.org/" "$limit" "-4"; then
            ipv4_ok=1
            print_success "Reachable via IPv4: proxy.golang.org"
        else
            print_error "Cannot reach https://proxy.golang.org/ via IPv4 within ${limit}s"
        fi

        # Short IPv6 probe — failure here is common on GCP and is not fatal if IPv4 works.
        if _go_probe_https "https://proxy.golang.org/" 5 "-6"; then
            ipv6_ok=1
            print_success "Reachable via IPv6: proxy.golang.org"
        else
            print_warning "IPv6 path to proxy.golang.org failed (will prefer IPv4 for Go if IPv4 works)"
        fi

        if [ "$ipv4_ok" -eq 1 ] && [ "$ipv6_ok" -eq 0 ]; then
            GO_FORCE_IPV4=1
            print_info "Broken IPv6 detected — Go module download will temporarily disable IPv6"
        fi

        if [ "$ipv4_ok" -ne 1 ]; then
            # Last resort: dual-stack (some hosts lack curl -4).
            if _go_probe_https "https://proxy.golang.org/" "$limit"; then
                print_success "Reachable (dual-stack): proxy.golang.org"
            else
                probe_status=1
            fi
        fi

        if ! _go_probe_https "https://sum.golang.org/" "$limit" "-4" \
            && ! _go_probe_https "https://sum.golang.org/" "$limit"; then
            print_error "Cannot reach https://sum.golang.org/ within ${limit}s"
            probe_status=1
        else
            print_success "Reachable: sum.golang.org"
        fi

        # GET a tiny known proxy path so HEAD-only reachability cannot hide a hung download path.
        if [ "$probe_status" -eq 0 ]; then
            print_info "Probing Go module proxy GET (sample @v/list)..."
            if ! curl -4 -sS -o /dev/null --connect-timeout "$limit" --max-time 30 \
                "https://proxy.golang.org/github.com/google/uuid/@v/list"; then
                print_error "Go module proxy GET probe failed within 30s"
                print_error "HEAD may work while module downloads hang — check firewall/DPI/IPv6"
                probe_status=1
            else
                print_success "Go module proxy GET probe OK"
            fi
        fi
    else
        if ! _go_probe_https "https://proxy.golang.org/" "$limit"; then
            print_error "Cannot reach https://proxy.golang.org/ within ${limit}s"
            probe_status=1
        else
            print_success "Reachable: proxy.golang.org"
        fi
        if ! _go_probe_https "https://sum.golang.org/" "$limit"; then
            print_error "Cannot reach https://sum.golang.org/ within ${limit}s"
            probe_status=1
        else
            print_success "Reachable: sum.golang.org"
        fi
    fi

    if [ "$probe_status" -ne 0 ]; then
        print_error "Outbound HTTPS to the Go module proxy/checksum DB is blocked or timing out"
        print_error "On cloud VMs check DNS, firewall/egress, IPv6 blackholes, and GOPROXY — then retry"
        print_error "Override example: GOPROXY=https://proxy.golang.org,direct"
        return 1
    fi
    return 0
}

# Kill a process group (negative PGID) with TERM then KILL.
_go_kill_process_group() {
    local pgid="$1"
    local label="${2:-process group}"

    if [ -z "$pgid" ] || [ "$pgid" -le 1 ] 2>/dev/null; then
        return 0
    fi
    print_error "Stopping ${label} (PGID ${pgid}) with SIGTERM..."
    kill -TERM -- "-${pgid}" 2>/dev/null || kill -TERM "$pgid" 2>/dev/null || true
    sleep 2
    if kill -0 "$pgid" 2>/dev/null; then
        print_error "Still alive — sending SIGKILL to PGID ${pgid}"
        kill -KILL -- "-${pgid}" 2>/dev/null || kill -KILL "$pgid" 2>/dev/null || true
    fi
}

# Run a Go command with a hard deadline and visible progress.
_run_go_command_bounded() {
    local label="$1"
    local deadline="$2"
    shift 2
    local command_pid=""
    local command_pgid=""
    local heartbeat_pid=""
    local elapsed=0
    local status=0
    local heartbeat_interval="${GO_COMMAND_HEARTBEAT_INTERVAL}"
    local kill_after="${GO_COMMAND_KILL_AFTER}"

    case "$heartbeat_interval" in
        ''|*[!0-9]*) heartbeat_interval=15 ;;
    esac
    case "$kill_after" in
        ''|*[!0-9]*) kill_after=15 ;;
    esac

    if command -v setsid &> /dev/null; then
        setsid "$@" &
    else
        "$@" &
    fi
    command_pid=$!

    if command -v ps &> /dev/null; then
        command_pgid=$(ps -o pgid= -p "$command_pid" 2>/dev/null | tr -d ' ' || true)
        case "$command_pgid" in
            ''|*[!0-9]*) command_pgid="" ;;
        esac
    fi

    print_info "${label} started (PID ${command_pid}, deadline ${deadline}s)"

    (
        heartbeat_elapsed=0
        while kill -0 "$command_pid" 2>/dev/null; do
            sleep "$heartbeat_interval"
            if ! kill -0 "$command_pid" 2>/dev/null; then
                break
            fi
            heartbeat_elapsed=$((heartbeat_elapsed + heartbeat_interval))
            printf 'ℹ  still %s... %ss elapsed (watchdog active)\n' \
                "$label" "$heartbeat_elapsed" >&2
        done
    ) &
    heartbeat_pid=$!

    while kill -0 "$command_pid" 2>/dev/null; do
        if [ "$elapsed" -ge "$deadline" ]; then
            print_error "${label} exceeded ${deadline}s"
            if [ -n "$command_pgid" ] && [ "$command_pgid" -gt 1 ] 2>/dev/null; then
                _go_kill_process_group "$command_pgid" "$label"
            else
                kill -TERM "$command_pid" 2>/dev/null || true
                sleep "$kill_after"
                if kill -0 "$command_pid" 2>/dev/null; then
                    kill -KILL "$command_pid" 2>/dev/null || true
                fi
            fi
            status=124
            break
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done

    if [ "$status" -ne 124 ]; then
        if wait "$command_pid"; then
            status=0
        else
            status=$?
        fi
    else
        wait "$command_pid" 2>/dev/null || true
    fi

    if [ -n "$heartbeat_pid" ]; then
        kill "$heartbeat_pid" 2>/dev/null || true
        wait "$heartbeat_pid" 2>/dev/null || true
    fi

    return "$status"
}

# Run go mod download with a hard bash deadline (primary guard).
# GNU timeout alone was insufficient on some cloud VMs (#371).
_run_go_mod_download_bounded() {
    local deadline="${GO_MODULE_DOWNLOAD_TIMEOUT}"
    local kill_after=15
    local status=0
    local download_pid=""
    local elapsed=0
    local heartbeat_pid=""

    _go_ipv4_force_begin
    _go_clear_stale_module_partials

    # Prefer IPv4 for Go's resolver when the host has broken AAAA routes (#371).
    export GODEBUG="${GODEBUG:+$GODEBUG,}netdns=go"

    # Heartbeat — no `local` inside non-function subshell (set -e).
    (
        elapsed=0
        while true; do
            sleep 15
            elapsed=$((elapsed + 15))
            printf 'ℹ  still downloading Go modules... %ss elapsed (watchdog active)\n' "$elapsed" >&2
        done
    ) &
    heartbeat_pid=$!

    # Run go as a direct background child of this shell (same user/session).
    # Prior setsid/pidfile races could exit the waiter early, restore IPv6, and
    # report success while go was still hung or already dead (#371 follow-up).
    (
        echo "go mod download child starting" >&2
        exec go mod download -x
    ) &
    download_pid=$!

    print_info "go mod download started (PID ${download_pid}, deadline ${deadline}s)"

    elapsed=0
    while kill -0 "$download_pid" 2>/dev/null; do
        if [ "$elapsed" -ge "$deadline" ]; then
            print_error "Go module download exceeded ${deadline}s"
            kill -TERM "$download_pid" 2>/dev/null || true
            sleep "$kill_after"
            if kill -0 "$download_pid" 2>/dev/null; then
                kill -KILL "$download_pid" 2>/dev/null || true
            fi
            status=124
            break
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done

    if [ "$status" -ne 124 ]; then
        if wait "$download_pid"; then
            status=0
        else
            status=$?
        fi
    else
        wait "$download_pid" 2>/dev/null || true
    fi

    if [ -n "$heartbeat_pid" ]; then
        kill "$heartbeat_pid" 2>/dev/null || true
        wait "$heartbeat_pid" 2>/dev/null || true
    fi

    _go_ipv4_force_end
    return "$status"
}

# embed_web_ui_build GO: copies the web app build (web-ui/build) into the
# server module, where the binary embeds it from. Without a build it makes
# one when Node.js and npm are installed; a build from source needs them.
embed_web_ui_build() {
    local go_bin="$1" web_ui="$SCRIPT_DIR/web-ui"
    if [ ! -f "$web_ui/build/index.html" ]; then
        if ! command -v npm &>/dev/null || [ ! -f "$web_ui/package-lock.json" ]; then
            print_error "A build from source needs the web app: $web_ui/build, or Node.js 22 and npm to build it"
            return 1
        fi
        print_step "Building the web app (npm, a few minutes)..."
        if ! (cd "$web_ui" && npx -y npm@11.6.2 ci --no-audit --no-fund && npm run build) > /tmp/betterdesk-web-ui-build.log 2>&1; then
            print_error "The web app build failed (log: /tmp/betterdesk-web-ui-build.log)"
            return 1
        fi
    fi
    if ! _run_go_command_bounded "embedding the web app" "$GO_MODULE_GRAPH_TIMEOUT" \
        "$go_bin" run ./cmd/embed-webui -from "$web_ui/build"; then
        print_error "Could not copy the web app build into the server module"
        return 1
    fi
    print_success "Web app build embedded"
}

compile_go_server() {
    print_step "Compiling BetterDesk Go server..."
    
    if [ ! -d "$GO_SERVER_SOURCE" ]; then
        print_error "Go server source not found: $GO_SERVER_SOURCE"
        return 1
    fi
    
    # Ensure Go is available
    export PATH=$PATH:/usr/local/go/bin
    
    if ! check_go_installed; then
        if ! install_golang; then
            print_error "Go is required for compilation"
            return 1
        fi
    fi
    
    cd "$GO_SERVER_SOURCE"
    
    # Clean previous builds
    rm -f betterdesk-server betterdesk-server-linux-*
    
    # Build
    print_info "Building BetterDesk server for $ARCH_NAME..."
    local output_name="betterdesk-server"
    local go_bin
    go_bin=$(command -v go)

    # Force local toolchain for native compile so host GOTOOLCHAIN=auto cannot
    # silently download another toolchain mid-install (hang risk on cloud VMs).
    # Override with BETTERDESK_GOTOOLCHAIN if needed. GOPROXY/GOSUMDB still honour env.
    export GOTOOLCHAIN="${BETTERDESK_GOTOOLCHAIN:-local}"
    export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
    export GOSUMDB="${GOSUMDB:-sum.golang.org}"

    print_info "Using $($go_bin version 2>/dev/null || echo 'unknown go')"
    print_info "GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB} GOTOOLCHAIN=${GOTOOLCHAIN}"

    if ! _go_module_network_preflight; then
        return 1
    fi

    # Download dependencies. Keep this visible and hard-bounded (bash watchdog).
    print_info "Downloading Go modules (timeout: ${GO_MODULE_DOWNLOAD_TIMEOUT}s, kill-after: 15s, process watchdog)..."
    local module_download_status=0
    # Keep the command in the if condition so `set -e` does not abort before
    # we can capture the actual watchdog/download exit status. Do not use `!`
    # here: inside its branch `$?` would be the status of the negation (0).
    if _run_go_mod_download_bounded; then
        module_download_status=0
    else
        module_download_status=$?
    fi

    if [ "$module_download_status" -ne 0 ]; then
        if [ "$module_download_status" -eq 124 ] || [ "$module_download_status" -eq 137 ]; then
            print_error "Go module download timed out after ${GO_MODULE_DOWNLOAD_TIMEOUT}s"
        else
            print_error "Go module download failed (exit code ${module_download_status})"
        fi
        print_error "Check DNS, outbound HTTPS to proxy.golang.org / sum.golang.org / go.dev, firewall and GOPROXY, then retry"
        print_error "On GCP/cloud VMs with broken IPv6, confirm IPv4 works: curl -4 -I https://proxy.golang.org/"
        return 1
    fi

    print_info "Go module download completed; validating module graph (timeout: ${GO_MODULE_GRAPH_TIMEOUT}s)..."
    local module_graph_status=0
    if _run_go_command_bounded "validating Go module graph" "$GO_MODULE_GRAPH_TIMEOUT" go list -m all; then
        module_graph_status=0
    else
        module_graph_status=$?
    fi
    if [ "$module_graph_status" -ne 0 ]; then
        if [ "$module_graph_status" -eq 124 ] || [ "$module_graph_status" -eq 137 ]; then
            print_error "Go module graph validation timed out after ${GO_MODULE_GRAPH_TIMEOUT}s"
        else
            print_error "Go module graph validation failed (exit code ${module_graph_status})"
        fi
        print_error "The module cache may be incomplete; retry the native installation after checking GOPROXY and disk space"
        return 1
    fi
    print_success "Go module graph validated"

    # The binary carries the web app: its build goes into the module first
    if ! embed_web_ui_build "$go_bin"; then
        return 1
    fi

    # Build with optimizations
    print_info "Building BetterDesk server (timeout: ${GO_BUILD_TIMEOUT}s)..."
    local build_status=0 commit
    commit=$(git -C "$SCRIPT_DIR" rev-parse HEAD 2>/dev/null || true)
    if _run_go_command_bounded "building Go server" "$GO_BUILD_TIMEOUT" \
        env CGO_ENABLED=0 "$go_bin" build -trimpath \
        -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${commit}" -o "$output_name" .; then
        build_status=0
    else
        build_status=$?
    fi
    if [ "$build_status" -ne 0 ]; then
        if [ "$build_status" -eq 124 ] || [ "$build_status" -eq 137 ]; then
            print_error "Go server build timed out after ${GO_BUILD_TIMEOUT}s"
        else
            print_error "Go server build failed (exit code ${build_status})"
        fi
        return 1
    fi
    
    if [ -f "$output_name" ]; then
        chmod +x "$output_name"
        local size=$(du -h "$output_name" | cut -f1)
        print_success "Compiled: $output_name ($size)"
        return 0
    else
        print_error "Compilation failed"
        return 1
    fi
}

#===============================================================================
# Installation Functions
#===============================================================================

install_dependencies() {
    print_step "Installing dependencies..."
    
    # The server is one static binary; the installer needs only these
    if command -v apt-get &> /dev/null; then
        apt-get update -qq
        apt-get install -y -qq sqlite3 curl wget openssl ca-certificates iproute2 lsof
    elif command -v dnf &> /dev/null; then
        dnf install -y -q sqlite curl wget openssl ca-certificates iproute lsof
    elif command -v yum &> /dev/null; then
        yum install -y -q sqlite curl wget openssl ca-certificates iproute lsof
    elif command -v pacman &> /dev/null; then
        pacman -Sy --noconfirm sqlite curl wget openssl ca-certificates iproute2 lsof
    else
        print_warning "Unknown package manager. Make sure curl, openssl and SQLite are installed."
    fi
    
    print_success "Dependencies installed"
}

#===============================================================================
# PostgreSQL Installation Functions
#===============================================================================

install_postgresql() {
    print_step "Installing PostgreSQL..."
    
    if command -v psql &> /dev/null; then
        local pg_version=$(psql --version | grep -oP '\d+' | head -1)
        print_success "PostgreSQL $pg_version already installed"
        return 0
    fi
    
    if command -v apt-get &> /dev/null; then
        apt-get update -qq
        apt-get install -y -qq postgresql postgresql-contrib
    elif command -v dnf &> /dev/null; then
        dnf install -y -q postgresql-server postgresql
        postgresql-setup --initdb 2>/dev/null || true
    elif command -v yum &> /dev/null; then
        yum install -y -q postgresql-server postgresql
        postgresql-setup initdb 2>/dev/null || true
    elif command -v pacman &> /dev/null; then
        pacman -Sy --noconfirm postgresql
        su - postgres -c "initdb -D /var/lib/postgres/data" 2>/dev/null || true
    else
        print_error "Cannot install PostgreSQL automatically."
        print_info "Please install PostgreSQL manually and run the script again."
        return 1
    fi
    
    # Start PostgreSQL service
    systemctl start postgresql 2>/dev/null || service postgresql start 2>/dev/null || true
    systemctl enable postgresql 2>/dev/null || true
    
    # Verify installation
    if command -v psql &> /dev/null; then
        print_success "PostgreSQL installed and started"
        return 0
    else
        print_error "PostgreSQL installation failed!"
        return 1
    fi
}

setup_postgresql_database() {
    print_step "Setting up PostgreSQL database for BetterDesk..."

    if ! is_valid_pg_identifier "$POSTGRESQL_USER"; then
        print_error "Invalid PostgreSQL username: $POSTGRESQL_USER"
        print_info "Allowed pattern: ^[A-Za-z_][A-Za-z0-9_]{0,62}$"
        return 1
    fi
    if ! is_valid_pg_identifier "$POSTGRESQL_DB"; then
        print_error "Invalid PostgreSQL database name: $POSTGRESQL_DB"
        print_info "Allowed pattern: ^[A-Za-z_][A-Za-z0-9_]{0,62}$"
        return 1
    fi
    
    # Generate password if not set
    if [ -z "$POSTGRESQL_PASS" ]; then
        # SECURITY (audit fix M-05, 2026-04-10): use hex (4 bits/char, no
        # alphabet shrinking) instead of base64+tr+truncate which lost a few
        # entropy bits per character.
        POSTGRESQL_PASS=$(openssl rand -hex 16)
        print_info "Generated PostgreSQL password"
    fi
    
    # Check if PostgreSQL is running
    if ! systemctl is-active --quiet postgresql 2>/dev/null; then
        systemctl start postgresql 2>/dev/null || service postgresql start 2>/dev/null
        sleep 2
    fi
    
    # Create user and database
    local pg_pass_sql
    pg_pass_sql=$(sql_escape_literal "$POSTGRESQL_PASS")

    print_step "Creating PostgreSQL user '$POSTGRESQL_USER'..."
    sudo -u postgres psql -v ON_ERROR_STOP=1 -c "CREATE USER \"$POSTGRESQL_USER\" WITH PASSWORD '$pg_pass_sql' CREATEDB;" 2>/dev/null || {
        print_warning "User might already exist, trying to update password..."
        sudo -u postgres psql -v ON_ERROR_STOP=1 -c "ALTER USER \"$POSTGRESQL_USER\" WITH PASSWORD '$pg_pass_sql';" 2>/dev/null || true
    }
    
    print_step "Creating PostgreSQL database '$POSTGRESQL_DB'..."
    POSTGRESQL_DB_CREATED=false
    if sudo -u postgres psql -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$POSTGRESQL_DB\" OWNER \"$POSTGRESQL_USER\";" 2>/dev/null; then
        POSTGRESQL_DB_CREATED=true
    else
        print_warning "Database might already exist"
    fi
    
    if ! ensure_postgresql_local_hba_rules; then
        print_error "PostgreSQL authentication setup failed"
        return 1
    fi

    # Build connection URI
    POSTGRESQL_URI="postgres://$POSTGRESQL_USER:$POSTGRESQL_PASS@$POSTGRESQL_HOST:$POSTGRESQL_PORT/$POSTGRESQL_DB?sslmode=disable"
    
    # Test connection
    print_step "Testing PostgreSQL connection..."
    if PGPASSWORD="$POSTGRESQL_PASS" psql -U "$POSTGRESQL_USER" -h "$POSTGRESQL_HOST" -p "$POSTGRESQL_PORT" -d "$POSTGRESQL_DB" -c "SELECT 1;" &>/dev/null; then
        print_success "PostgreSQL connection successful!"
        print_info "Connection URI: postgres://$POSTGRESQL_USER:****@$POSTGRESQL_HOST:$POSTGRESQL_PORT/$POSTGRESQL_DB"
    else
        print_error "PostgreSQL connection failed!"
        print_info "Check PostgreSQL pg_hba.conf for local connections"
        return 1
    fi
    
    return 0
}

choose_database_type() {
    if [ "$AUTO_MODE" = true ]; then
        # In auto mode, use environment variable or default to SQLite
        if [ "$USE_POSTGRESQL" = "true" ]; then
            print_info "Auto mode: Using PostgreSQL"
            return 0
        else
            print_info "Auto mode: Using SQLite (default)"
            USE_POSTGRESQL="false"
            return 0
        fi
    fi
    
    echo ""
    local _menu_items=(
        $'SQLite (default)\tSingle-file DB, zero setup, good for <=100 devices'
        $'PostgreSQL (production)\tPooled SQL DB, multi-server / >100 devices / HA'
    )
    local _menu_returns=( 1 2 )
    menu_choose "Select Database Type" "SQLite is recommended for most installs"
    local db_choice="${MENU_CHOICE:-1}"
    
    case $db_choice in
        2)
            USE_POSTGRESQL="true"
            print_info "Selected: PostgreSQL"
            
            # Ask for PostgreSQL details or use defaults
            echo ""
            read -p "PostgreSQL host [$POSTGRESQL_HOST]: " pg_host
            POSTGRESQL_HOST="${pg_host:-$POSTGRESQL_HOST}"
            
            read -p "PostgreSQL port [$POSTGRESQL_PORT]: " pg_port
            POSTGRESQL_PORT="${pg_port:-$POSTGRESQL_PORT}"
            
            read -p "PostgreSQL database [$POSTGRESQL_DB]: " pg_db
            POSTGRESQL_DB="${pg_db:-$POSTGRESQL_DB}"
            
            read -p "PostgreSQL user [$POSTGRESQL_USER]: " pg_user
            POSTGRESQL_USER="${pg_user:-$POSTGRESQL_USER}"
            
            read -sp "PostgreSQL password (leave empty to generate): " pg_pass
            echo ""
            POSTGRESQL_PASS="${pg_pass:-}"
            ;;
        *)
            USE_POSTGRESQL="false"
            print_info "Selected: SQLite"
            ;;
    esac
}

migrate_sqlite_to_postgresql() {
    print_step "Migrating existing SQLite data to PostgreSQL..."
    
    local sqlite_db="$RUSTDESK_PATH/db_v2.sqlite3"
    
    if [ ! -f "$sqlite_db" ]; then
        print_info "No existing SQLite database found, skipping migration"
        return 0
    fi
    
    # Find migration binary
    local migrate_bin=""
    if [ -f "$SCRIPT_DIR/betterdesk-server/tools/migrate/migrate-linux-amd64" ]; then
        migrate_bin="$SCRIPT_DIR/betterdesk-server/tools/migrate/migrate-linux-amd64"
    elif [ -f "$SCRIPT_DIR/tools/migrate/migrate-linux-amd64" ]; then
        migrate_bin="$SCRIPT_DIR/tools/migrate/migrate-linux-amd64"
    elif [ -f "/opt/betterdesk-go/migrate" ]; then
        migrate_bin="/opt/betterdesk-go/migrate"
    fi
    
    # Try to compile migration tool from source if not found or outdated
    if [ -z "$migrate_bin" ] && command -v go &>/dev/null; then
        local migrate_src="$SCRIPT_DIR/betterdesk-server/tools/migrate"
        if [ -d "$migrate_src" ]; then
            print_info "Compiling migration tool from source..."
            if (cd "$SCRIPT_DIR/betterdesk-server" && go build -o "tools/migrate/migrate-linux-amd64" ./tools/migrate/) 2>&1; then
                migrate_bin="$migrate_src/migrate-linux-amd64"
                print_success "Migration tool compiled successfully"
            else
                print_warning "Failed to compile migration tool"
            fi
        fi
    fi
    
    if [ -z "$migrate_bin" ]; then
        print_warning "Migration binary not found: the server keeps using $sqlite_db"
        print_info "Move it later with: M -> 3 (SQLite → PostgreSQL)"
        keep_sqlite_install "$sqlite_db"
        return 0
    fi
    
    chmod +x "$migrate_bin"
    
    # Verify binary supports -mode flag (in case of outdated binary). The
    # help is read whole first: grep -q on a pipe can end the tool early
    local migrate_help
    migrate_help=$("$migrate_bin" -h 2>&1 || true)
    if ! grep -q -- "-mode" <<< "$migrate_help"; then
        print_warning "Migration binary is outdated (missing -mode flag): the server keeps using $sqlite_db"
        print_info "Rebuild with: cd betterdesk-server && go build -o tools/migrate/migrate-linux-amd64 ./tools/migrate/"
        keep_sqlite_install "$sqlite_db"
        return 0
    fi
    
    if [ "$AUTO_MODE" != true ] && ! confirm "Move the existing SQLite install (every server and console table) to PostgreSQL?"; then
        print_info "The install stays on SQLite ($sqlite_db); move it later with the menu's Database migration"
        keep_sqlite_install "$sqlite_db"
        return 0
    fi

    # -overwrite only for a database this run created itself, or after the
    # user agreed: otherwise the tool refuses a database with rows in it
    local overwrite=""
    if [ "$POSTGRESQL_DB_CREATED" = true ]; then
        overwrite="-overwrite"
    elif [ "$AUTO_MODE" != true ] && confirm "If the PostgreSQL database already holds data, replace all of it?"; then
        overwrite="-overwrite"
    fi

    print_step "Creating backup before migration..."
    "$migrate_bin" -mode backup -src "$sqlite_db" 2>&1 || true

    print_step "Moving the SQLite install to PostgreSQL..."
    if "$migrate_bin" -mode sqlite2pg -src "$sqlite_db" -dst "$POSTGRESQL_URI" ${overwrite:+"$overwrite"} 2>&1; then
        use_database_in_env postgres "$POSTGRESQL_URI"
        print_success "The install now runs on PostgreSQL"
    else
        print_warning "The move to PostgreSQL failed (output above): the server keeps using $sqlite_db"
        keep_sqlite_install "$sqlite_db"
    fi
}

# use_database_in_env postgres URI | sqlite PATH: points the console's .env
# (DB_TYPE, DATABASE_URL, DB_URL) at the database the server is to use.
use_database_in_env() {
    local env_file="$CONSOLE_PATH/.env" key url=""
    [ -f "$env_file" ] || return 0
    [ "$1" = postgres ] && url="$2"
    # The lines are written anew instead of edited with sed, so an address
    # with & or | in it stays as it is
    for key in DB_TYPE DATABASE_URL DB_URL; do
        _remove_env_line "$env_file" "$key"
    done
    [ -z "$(tail -c1 "$env_file")" ] || echo >> "$env_file"
    printf 'DB_TYPE=%s\nDATABASE_URL=%s\nDB_URL=%s\n' "$1" "$url" "$2" >> "$env_file"
}

# keep_sqlite_install PATH: the install stays on its SQLite file after the
# move to PostgreSQL did not happen.
keep_sqlite_install() {
    USE_POSTGRESQL="false"
    POSTGRESQL_URI=""
    use_database_in_env sqlite "$1"
}

#===============================================================================
# The server binary and the console's folder
#===============================================================================

# The release file of this machine's binary.
release_asset_name() {
    case "$(uname -m)" in
        x86_64|amd64) echo "betterdesk-server-linux-amd64" ;;
        aarch64|arm64) echo "betterdesk-server-linux-arm64" ;;
        armv7l|armv7|armhf) echo "betterdesk-server-linux-armv7" ;;
        *) return 1 ;;
    esac
}

# Where the release files are: RELEASE_BASE_URL, else the newest release
# (stable channel, branch main) or the "dev" pre-release (development
# channel, branch dev) of UPDATE_GITHUB_OWNER/UPDATE_GITHUB_REPO.
release_base_url() {
    if [ -n "${RELEASE_BASE_URL:-}" ]; then
        printf '%s\n' "${RELEASE_BASE_URL%/}"
        return
    fi
    local releases="https://github.com/${UPDATE_GITHUB_OWNER}/${UPDATE_GITHUB_REPO}/releases"
    if [ "$UPDATE_GITHUB_BRANCH" = "dev" ]; then
        printf '%s\n' "$releases/download/dev"
    else
        printf '%s\n' "$releases/latest/download"
    fi
}

# download_release_binary DIR: the release binary for this machine into
# DIR/betterdesk-server, checked against the release's SHA256SUMS.
download_release_binary() {
    local dir="$1" asset base expected actual
    asset=$(release_asset_name) || {
        print_error "There is no release binary for $(uname -m); use --from-source"
        return 1
    }
    read_update_github_branch_from_env
    base=$(release_base_url)
    print_info "Downloading $asset ($UPDATE_GITHUB_BRANCH channel) from $base"
    local code
    code=$(curl -sSL --retry 3 --connect-timeout 20 -o "$dir/SHA256SUMS" -w '%{http_code}' "$base/SHA256SUMS" 2>/dev/null) || true
    case "$code" in
        200) ;;
        000|"")
            print_error "Could not reach $base"
            print_info "Check this server's internet access and DNS (a proxy goes in HTTPS_PROXY),"
            print_info "or install offline: download the binary elsewhere and run with --binary FILE"
            return 1
            ;;
        404)
            if [ -z "${RELEASE_BASE_URL:-}" ] && [ "$UPDATE_GITHUB_BRANCH" != dev ]; then
                print_error "${UPDATE_GITHUB_OWNER}/${UPDATE_GITHUB_REPO} has no stable release yet"
                print_info "Install the development build with --channel dev, or a binary you have with --binary FILE"
            else
                print_error "There is no release at $base"
                print_info "Install a binary you have with --binary FILE"
            fi
            return 1
            ;;
        *)
            print_error "Downloading $base/SHA256SUMS failed (HTTP $code); try again later, or use --binary FILE"
            return 1
            ;;
    esac
    local progress=(-s)
    [ -t 1 ] && progress=(--progress-bar)
    if ! curl -fSL --retry 3 --connect-timeout 20 "${progress[@]}" -o "$dir/$asset" "$base/$asset"; then
        print_error "Could not download $base/$asset"
        return 1
    fi
    expected=$(awk -v name="$asset" '$2 == name || $2 == "*" name { print tolower($1); exit }' "$dir/SHA256SUMS")
    actual=$(sha256sum "$dir/$asset" | awk '{ print $1 }')
    if [ "$SKIP_VERIFY" = true ]; then
        print_warning "SHA-256 check skipped (--skip-verify)"
    elif [ -z "$expected" ]; then
        print_error "SHA256SUMS of the release does not list $asset"
        return 1
    elif [ "$actual" != "$expected" ]; then
        print_error "SHA-256 of $asset does not match the release's SHA256SUMS"
        return 1
    else
        print_success "SHA-256 of $asset matches the release"
    fi
    mv -f "$dir/$asset" "$dir/betterdesk-server"
}

# build_server_from_source DEST: builds betterdesk-server from this checkout,
# with the web app built in, into DEST.
build_server_from_source() {
    local dest="$1"
    detect_architecture
    if ! compile_go_server; then
        print_error "Failed to build betterdesk-server from $GO_SERVER_SOURCE"
        return 1
    fi
    cp "$GO_SERVER_SOURCE/betterdesk-server" "$dest"
}

# obtain_server_binary: a checked binary for this machine, from --binary,
# --from-source or the release, at STAGED_SERVER_BINARY in a temporary
# folder STAGED_SERVER_DIR that the caller removes.
obtain_server_binary() {
    STAGED_SERVER_DIR=$(mktemp -d /tmp/betterdesk-server.XXXXXX)
    STAGED_SERVER_BINARY="$STAGED_SERVER_DIR/betterdesk-server"
    if [ -n "$SERVER_BINARY_FILE" ]; then
        print_info "Using the binary $SERVER_BINARY_FILE"
        cp "$SERVER_BINARY_FILE" "$STAGED_SERVER_BINARY" || return 1
    elif [ "$FROM_SOURCE" = true ]; then
        build_server_from_source "$STAGED_SERVER_BINARY" || return 1
    else
        download_release_binary "$STAGED_SERVER_DIR" || return 1
    fi
    chmod 0755 "$STAGED_SERVER_BINARY"
    local line
    if ! line=$(timeout 10 "$STAGED_SERVER_BINARY" -version 2>/dev/null); then
        print_error "The new binary does not run on this machine"
        return 1
    fi
    if ! binary_has_subcommands "$STAGED_SERVER_BINARY"; then
        print_error "The new binary is not a release with the web console inside ($line)"
        return 1
    fi
    STAGED_SERVER_VERSION="$line"
    print_success "New binary: $line"
}

# install_staged_binary [SUFFIX]: puts STAGED_SERVER_BINARY in place of the
# installed binary and keeps the old one as betterdesk-server<SUFFIX>
# (.prev unless given). The service must be stopped.
install_staged_binary() {
    local suffix="${1:-.prev}" bin
    bin=$(server_binary_path)
    mkdir -p "$RUSTDESK_PATH"
    if [ -f "$bin" ]; then
        cp -p "$bin" "$bin$suffix"
    fi
    if ! install -m 0755 "$STAGED_SERVER_BINARY" "$bin.new.$$" || ! mv -f "$bin.new.$$" "$bin"; then
        rm -f "$bin.new.$$"
        print_error "Could not install $bin"
        return 1
    fi
    print_success "Installed $bin"
}

install_binaries() {
    print_step "Installing betterdesk-server..."
    obtain_server_binary || { rm -rf "${STAGED_SERVER_DIR:-}"; return 1; }
    if systemctl is-active --quiet "$SERVER_SERVICE" 2>/dev/null; then
        graceful_stop_services
    fi
    install_staged_binary || { rm -rf "$STAGED_SERVER_DIR"; return 1; }
    rm -rf "$STAGED_SERVER_DIR"
}

# The account the server runs as.
ensure_service_user() {
    if ! id "$SERVICE_USER" &>/dev/null; then
        useradd -r -s /usr/sbin/nologin -d /var/lib/betterdesk -c "BetterDesk server" "$SERVICE_USER" || {
            print_error "Could not create the system user $SERVICE_USER"
            return 1
        }
    fi
    mkdir -p /var/lib/betterdesk
    chown "$SERVICE_USER:$SERVICE_USER" /var/lib/betterdesk 2>/dev/null || true
}

# own_by_service_user PATH...: hands files and folders below PATH to the
# service account, touching only those another account owns.
own_by_service_user() {
    find "$@" \( ! -user "$SERVICE_USER" -o ! -group "$SERVICE_USER" \) \
        -exec chown -h "$SERVICE_USER:$SERVICE_USER" {} + 2>/dev/null || true
}

# The server folder belongs to root with the service's group, which may
# write it, and nobody else may list or read it (2770): the server makes
# its database, keys and the binary's .prev there. Its files belong to the service account, except the copies
# kept for a rollback, the admin credentials and a source checkout.
set_server_folder_permissions() {
    mkdir -p "$RUSTDESK_PATH/ssl"
    chown root:"$SERVICE_USER" "$RUSTDESK_PATH" 2>/dev/null || true
    chmod 2770 "$RUSTDESK_PATH" 2>/dev/null || true
    local -a entries
    mapfile -t entries < <(find "$RUSTDESK_PATH" -mindepth 1 -maxdepth 1 ! -name '.admin_credentials' \
        ! -name '*.pre-go' ! -name 'source')
    [ "${#entries[@]}" -gt 0 ] && own_by_service_user "${entries[@]}"
    chmod 750 "$RUSTDESK_PATH/ssl" 2>/dev/null || true
    local f
    for f in .api_key id_ed25519 ssl/betterdesk.key; do
        [ -f "$RUSTDESK_PATH/$f" ] && chmod 600 "$RUSTDESK_PATH/$f"
    done
    return 0
}

# setup_console_home: the console's folder with its .env and data folder.
setup_console_home() {
    print_step "Setting up the web console's folder $CONSOLE_PATH..."
    ensure_service_user || return 1
    mkdir -p "$CONSOLE_PATH/data"

    local is_fresh=false
    if [ ! -f "$CONSOLE_PATH/.env" ]; then
        is_fresh=true
    fi
    if [ "$is_fresh" = true ]; then
        if [ -z "$ADMIN_PASSWORD" ]; then
            ADMIN_PASSWORD=$(openssl rand -hex 16)
        else
            print_info "Using custom admin password from ADMIN_PASSWORD env var"
        fi
        touch "$CONSOLE_PATH/data/.force_password_update"
    else
        print_info "Keeping the existing .env and accounts"
        ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
    fi

    merge_console_env "$is_fresh" || return 1
    if [ "$MINIMAL_MODE" = true ]; then
        _upsert_env_line "$CONSOLE_PATH/.env" CONSOLE_PORT 0
        print_info "Minimal installation: the web console stays off (CONSOLE_PORT=0)"
    fi

    if [ -d "$SCRIPT_DIR/remote-gateway" ]; then
        mkdir -p "$CONSOLE_PATH/remote-gateway"
        cp -a "$SCRIPT_DIR/remote-gateway/." "$CONSOLE_PATH/remote-gateway/"
    fi
    if [ -f "$SCRIPT_DIR/VERSION" ]; then
        cp -a "$SCRIPT_DIR/VERSION" "$CONSOLE_PATH/VERSION" 2>/dev/null || true
    fi

    if [ "$STORE_ADMIN_CREDENTIALS" = "true" ] && [ "$is_fresh" = true ] && [ -n "$ADMIN_PASSWORD" ]; then
        cat > "$CONSOLE_PATH/data/.admin_credentials" << CREDEOF
Admin Username: admin
Admin Password: $ADMIN_PASSWORD
Generated by: BetterDesk installer
Timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)
CREDEOF
    fi

    own_by_service_user "$CONSOLE_PATH"
    chmod 750 "$CONSOLE_PATH"
    chmod 700 "$CONSOLE_PATH/data"
    chmod 600 "$CONSOLE_PATH/.env"
    if [ -f "$CONSOLE_PATH/data/.admin_credentials" ]; then
        chown root:root "$CONSOLE_PATH/data/.admin_credentials"
        chmod 600 "$CONSOLE_PATH/data/.admin_credentials"
    fi
    CONSOLE_TYPE="go"
    print_success "Web console folder ready"
}

generate_ssl_certificates() {
    print_step "Generating self-signed TLS certificates..."
    
    local ssl_dir="$RUSTDESK_PATH/ssl"
    
    # Skip if certificates already exist
    if [ -f "$ssl_dir/betterdesk.crt" ] && [ -f "$ssl_dir/betterdesk.key" ]; then
        print_info "TLS certificates already exist at $ssl_dir"
        print_info "Skipping certificate generation (use SSL config menu to regenerate)"
        return 0
    fi
    
    # Ensure openssl is available
    if ! command -v openssl &>/dev/null; then
        print_warning "openssl not found - skipping TLS certificate generation"
        print_info "Install openssl and use SSL config menu (option C) to generate later"
        return 1
    fi
    
    mkdir -p "$ssl_dir"
    
    # Detect server IPs for SAN (Subject Alternative Name)
    local server_ip
    server_ip=$(get_public_ip)
    
    # Detect LAN IP (first non-loopback IPv4)
    local lan_ip
    lan_ip=$(ip -4 addr show scope global | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -1 2>/dev/null || \
             hostname -I 2>/dev/null | awk '{print $1}' || echo "")
    
    # Build SAN list
    local san_list="IP:$server_ip,IP:127.0.0.1,DNS:localhost"
    
    # Add LAN IP if different from public IP
    if [ -n "$lan_ip" ] && [ "$lan_ip" != "$server_ip" ] && [ "$lan_ip" != "127.0.0.1" ]; then
        san_list="$san_list,IP:$lan_ip"
    fi
    
    # Add custom domain if provided via environment variable
    if [ -n "${SSL_DOMAIN:-}" ]; then
        san_list="DNS:$SSL_DOMAIN,$san_list"
        print_info "Adding domain to certificate: $SSL_DOMAIN"
    fi
    
    # Determine CN (Common Name) — prefer domain, fallback to public IP
    local cn="${SSL_DOMAIN:-$server_ip}"
    
    # Generate certificate with SAN extension (valid for 10 years for self-signed)
    openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
        -keyout "$ssl_dir/betterdesk.key" \
        -out "$ssl_dir/betterdesk.crt" \
        -subj "/CN=$cn/O=BetterDesk/C=PL" \
        -addext "subjectAltName=$san_list" \
        2>&1 || {
        print_warning "Certificate generation failed (openssl too old for -addext?)"
        # Fallback without SAN for older openssl
        openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
            -keyout "$ssl_dir/betterdesk.key" \
            -out "$ssl_dir/betterdesk.crt" \
            -subj "/CN=$cn/O=BetterDesk/C=PL" \
            2>&1 || {
            print_error "Failed to generate self-signed certificate"
            return 1
        }
    }
    
    # Deploy with console-user-readable permissions (#219)
    if ! deploy_ssl_material_to_rustdesk_dir "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key"; then
        print_error "Failed to set permissions on self-signed certificate"
        return 1
    fi
    
    if [ -d "$CONSOLE_PATH" ]; then
        # HTTPS for the web console (5443, a redirect on its HTTP port) and
        # the client API port
        local env_file="$CONSOLE_PATH/.env"
        if [ -f "$env_file" ]; then
            sed -i "s|^HTTPS_ENABLED=.*|HTTPS_ENABLED=true|" "$env_file"
            sed -i "s|^SSL_CERT_PATH=.*|SSL_CERT_PATH=$ssl_dir/betterdesk.crt|" "$env_file"
            sed -i "s|^SSL_KEY_PATH=.*|SSL_KEY_PATH=$ssl_dir/betterdesk.key|" "$env_file"
            # The internal API URLs stay http://: API TLS breaks RustDesk clients.

            # Enterprise TLS compatibility: Go API must remain HTTP because
            # RustDesk desktop clients use plain HTTP on signal_port-2.
            if [ "${ENTERPRISE_TLS:-false}" = "true" ]; then
                if grep -q '^ALLOW_SELF_SIGNED_CERTS=' "$env_file" 2>/dev/null; then
                    sed -i "s|^ALLOW_SELF_SIGNED_CERTS=.*|ALLOW_SELF_SIGNED_CERTS=true|" "$env_file"
                else
                    echo "ALLOW_SELF_SIGNED_CERTS=true" >> "$env_file"
                fi
                sed -i "s|^HBBS_API_URL=https://localhost|HBBS_API_URL=http://localhost|" "$env_file"
                sed -i "s|^BETTERDESK_API_URL=https://localhost|BETTERDESK_API_URL=http://localhost|" "$env_file"
                print_info "Enterprise TLS: Go API stays HTTP for RustDesk client compatibility"
            fi
        fi
    fi
    
    print_success "Self-signed TLS certificate generated (valid 10 years)"
    print_info "Certificate: $ssl_dir/betterdesk.crt"
    print_info "Private key: $ssl_dir/betterdesk.key"
    print_info "SANs: $san_list"
    [ -n "$lan_ip" ] && [ "$lan_ip" != "$server_ip" ] && print_info "LAN IP included: $lan_ip"
    return 0
}

#===============================================================================
# The service: betterdesk-server with the web console inside
#===============================================================================

# env_file_value FILE KEY: the value the first line setting KEY has, trimmed.
env_file_value() {
    [ -f "$1" ] || return 0
    { grep -m1 "^$2=" "$1" 2>/dev/null || true; } | cut -d= -f2- | tr -d '\r' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//'
}

# unit_environment UNIT: the KEY=VALUE pairs of a unit's Environment= lines,
# one per line, with systemd's quotes and its $$ and %% escapes undone.
unit_environment() {
    local line rest
    local -a words
    [ -f "$1" ] || return 0
    while IFS= read -r line; do
        rest="${line#Environment=}"
        if [[ "$rest" == \"*\" ]]; then
            rest="${rest#\"}"
            printf '%s\n' "${rest%\"}"
        else
            read -r -a words <<< "$rest"
            [ "${#words[@]}" -gt 0 ] && printf '%s\n' "${words[@]}"
        fi
    done < <(grep -E '^Environment=' "$1") | sed 's/\$\$/$/g; s/%%/%/g'
}

# unit_env_value UNIT KEY: the value a unit's Environment= lines give KEY.
unit_env_value() {
    unit_environment "$1" | awk -v key="$2" 'index($0, key "=") == 1 { value = substr($0, length(key) + 2) } END { if (value != "") print value }'
}

# unit_exec_binary UNIT: the program a unit's ExecStart runs.
unit_exec_binary() {
    grep -m1 '^ExecStart=' "$1" 2>/dev/null | sed -E 's/^ExecStart=[-@+!:]*//; s/[[:space:]].*//'
}

# unit_exec_flag_value UNIT FLAG: the value of a flag on a unit's ExecStart
# line, quoted or not, with systemd's escapes undone.
unit_exec_flag_value() {
    grep -m1 '^ExecStart=' "$1" 2>/dev/null \
        | sed -nE "s/.*[[:space:]]$2[[:space:]]+\"([^\"]*)\".*/\\1/p; t; s/.*[[:space:]]$2[[:space:]]+([^[:space:]]+).*/\\1/p" \
        | head -1 | sed 's/\$\$/$/g; s/%%/%/g'
}

# unit_exec_args UNIT: the arguments of a unit's ExecStart after the
# program, without -db and -init-admin-pass (the env file carries them as
# DB_URL and INIT_ADMIN_PASS), without the API TLS flags RustDesk clients
# cannot use, and with the old client API port as -api-port moved to
# GO_API_PORT (the web console serves 21121).
unit_exec_args() {
    grep -m1 '^ExecStart=' "$1" 2>/dev/null \
        | sed -E 's/^ExecStart=[-@+!:]*[^[:space:]]+//' \
        | sed -E 's/[[:space:]]-db[[:space:]]+"[^"]*"//; s/[[:space:]]-db[[:space:]]+[^[:space:]]+//' \
        | sed -E 's/[[:space:]]-init-admin-pass[[:space:]]+[^[:space:]]+//' \
        | sed -E 's/[[:space:]]-tls-api-port(=[^[:space:]]*)?//g; s/[[:space:]]-tls-api(=[^[:space:]]*)?//g' \
        | sed -E "s/-api-port[[:space:]]+${CLIENT_API_PORT:-21121}([[:space:]]|\$)/-api-port ${GO_API_PORT:-21114}\\1/" \
        | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//; s/[[:space:]]+/ /g'
}

# The settings write_server_unit writes itself, and the ports that live in
# .env (carry_unit_ports moves them there).
SERVER_UNIT_OWN_KEYS=" BETTERDESK_ENV_FILE BETTERDESK_SUPERVISOR SIGNAL_PORT RELAY_PORT GO_API_PORT MESH_ENABLED CONSOLE_PORT "

# server_unit_extra_environment: the Environment= lines of the installed
# unit besides the ones write_server_unit writes (connection mode, proxy
# trust), so a rewritten unit keeps them.
server_unit_extra_environment() {
    local pair key
    is_go_only_install || return 0
    while IFS= read -r pair; do
        key="${pair%%=*}"
        case "$SERVER_UNIT_OWN_KEYS" in *" $key "*) continue ;; esac
        printf 'Environment=%s\n' "$(printf '%s' "$pair" | sed 's/\$/$$/g; s/%/%%/g')"
    done < <(unit_environment "$SERVER_UNIT")
}

# True when the web console listens on a port below 1024.
console_uses_privileged_port() {
    local key port
    for key in CONSOLE_PORT HTTPS_PORT API_PORT; do
        port=$(env_file_value "$CONSOLE_PATH/.env" "$key")
        if [[ "$port" =~ ^[0-9]+$ ]] && [ "$port" -gt 0 ] && [ "$port" -lt 1024 ]; then
            return 0
        fi
    done
    return 1
}

# write_server_unit ARGS EXTRA_ENV: betterdesk-server.service, the one
# process that serves signal, relay, API and the web console, as the
# service account. ARGS are the server's arguments; EXTRA_ENV are more
# Environment= lines. The server reads the console's .env itself
# (BETTERDESK_ENV_FILE) and fills only what the unit leaves unset. Exit
# status 3 is the server's request to be started again (a restart, an
# update or a restore from the panel).
write_server_unit() {
    local args="$1" extra_env="${2:-}" bind_cap=""
    if console_uses_privileged_port; then
        bind_cap="AmbientCapabilities=CAP_NET_BIND_SERVICE"
    fi
    # Lines of the template that stay empty are left out
    cat << EOF | grep -v '^__EMPTY__$' > "$SERVER_UNIT"
[Unit]
Description=BetterDesk server and web console v$VERSION
Documentation=https://github.com/trybetterdesk/betterdesk
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$RUSTDESK_PATH
Environment=BETTERDESK_ENV_FILE=$CONSOLE_PATH/.env
Environment=BETTERDESK_SUPERVISOR=systemd
Environment=GO_API_PORT=${GO_API_PORT:-21114}
Environment=MESH_ENABLED=Y
${extra_env:-__EMPTY__}
ExecStart=$(server_binary_path) $args
Restart=always
RestartSec=2
SuccessExitStatus=3
LimitNOFILE=1000000
NoNewPrivileges=yes
PrivateTmp=yes
${bind_cap:-__EMPTY__}

[Install]
WantedBy=multi-user.target
EOF
    chmod 0644 "$SERVER_UNIT"
}

# Removes the units of retired BetterDesk services (Rust signal and relay,
# Flask console, manual installs).
remove_retired_units() {
    local svc
    for svc in rustdesksignal rustdeskrelay betterdesk-api betterdesk-go betterdesk; do
        if [ -f "/etc/systemd/system/$svc.service" ]; then
            systemctl stop "$svc" 2>/dev/null || true
            systemctl disable "$svc" 2>/dev/null || true
            rm -f "/etc/systemd/system/$svc.service"
            print_info "Removed retired $svc.service"
        fi
    done
}

setup_services() {
    print_step "Configuring the systemd service..."

    # The database settings from an existing .env, so a reinstall keeps
    # PostgreSQL
    if [ "$USE_POSTGRESQL" != "true" ] && [ -f "$CONSOLE_PATH/.env" ]; then
        if [ "$(env_file_value "$CONSOLE_PATH/.env" DB_TYPE)" = "postgres" ]; then
            POSTGRESQL_URI=$(env_file_value "$CONSOLE_PATH/.env" DATABASE_URL)
            if [ -n "$POSTGRESQL_URI" ]; then
                USE_POSTGRESQL="true"
                print_info "Recovered PostgreSQL config from existing .env"
            fi
        fi
    fi

    # Relay address: RELAY_SERVERS, else the mode (auto = public IP, local =
    # LAN IP), asked for in an interactive run
    if [ "$AUTO_MODE" = false ] && [ -z "$RELAY_SERVERS" ] && [ "${RELAY_MODE:-auto}" = "auto" ]; then
        local _local_ip
        _local_ip=$(get_local_ip)
        echo ""
        print_info "Relay server address controls how clients connect for remote sessions."
        echo -e "  ${CYAN}1)${NC} Internet / public  ${DIM}(auto-detect public IP — default)${NC}"
        echo -e "  ${CYAN}2)${NC} LAN only           ${DIM}(use this server's local IP: $_local_ip)${NC}"
        echo -e "  ${CYAN}3)${NC} Custom address     ${DIM}(enter a specific IP or host)${NC}"
        echo -ne "  ${CYAN}Select relay mode [1]:${NC} "
        read -r _relay_choice
        case "$_relay_choice" in
            2) RELAY_MODE="local" ;;
            3)
                echo -ne "  ${CYAN}Enter relay address (IP or host[:port]):${NC} "
                read -r RELAY_SERVERS
                ;;
            *) RELAY_MODE="auto" ;;
        esac
        echo ""
    fi

    local server_ip
    server_ip=$(resolve_relay_ip)
    resolve_connection_mode_env
    print_info "Relay server IP: $server_ip (mode: ${RELAY_SERVERS:+fixed}${RELAY_SERVERS:-$RELAY_MODE})"

    local signal_rate_limit="${SIGNAL_RATE_LIMIT_PER_IP:-20}"
    if ! [[ "$signal_rate_limit" =~ ^[0-9]+$ ]]; then
        print_warning "Invalid SIGNAL_RATE_LIMIT_PER_IP='$signal_rate_limit'; using 20"
        signal_rate_limit="20"
    fi

    # TLS for signal and relay when certificates exist. The API port stays
    # HTTP: RustDesk desktop clients call it without TLS.
    local tls_arg=""
    local ssl_dir="$RUSTDESK_PATH/ssl"
    if [ -f "$ssl_dir/betterdesk.crt" ] && [ -f "$ssl_dir/betterdesk.key" ]; then
        tls_arg=" -tls-cert $ssl_dir/betterdesk.crt -tls-key $ssl_dir/betterdesk.key -tls-signal -tls-relay"
        print_info "TLS: on for signal and relay (API stays HTTP)"
    fi

    if [ ! -s "$RUSTDESK_PATH/.api_key" ]; then
        openssl rand -hex 32 > "$RUSTDESK_PATH/.api_key"
        chmod 600 "$RUSTDESK_PATH/.api_key"
    fi

    write_server_unit "-mode all -relay-servers $server_ip -key-file $RUSTDESK_PATH/id_ed25519 -api-port ${GO_API_PORT:-21114} -signal-rate-limit-per-ip $signal_rate_limit$tls_arg" \
        "$CONNECTION_MODE_ENV_BLOCK"
    remove_retired_units
    systemctl daemon-reload
    print_success "Created betterdesk-server.service (server and web console, user $SERVICE_USER)"
}

# carry_unit_ports: the signal and relay ports an older unit set (they won
# over .env) go into .env, where the admin changes them, so a unit written
# without them keeps the ports the server listens on.
carry_unit_ports() {
    local key value
    for key in SIGNAL_PORT RELAY_PORT; do
        value=$(unit_env_value "$SERVER_UNIT" "$key")
        [ -n "$value" ] && _upsert_env_line "$CONSOLE_PATH/.env" "$key" "$value"
    done
    return 0
}

# During UPDATE and REPAIR: write the unit again from the template with the
# installed unit's arguments and extra settings; make it when missing.
maybe_update_services() {
    if ! is_go_only_install; then
        setup_services
        return
    fi
    local args extra
    args=$(unit_exec_args "$SERVER_UNIT")
    extra=$(server_unit_extra_environment)
    carry_unit_ports
    write_server_unit "$args" "$extra"
    systemctl daemon-reload
    print_info "betterdesk-server.service written again with its arguments and settings"
}

# The port the web console answers /health on: HTTPS_PORT when HTTPS is on
# and its certificate is there, else CONSOLE_PORT. Empty when the console
# is off.
console_health_url() {
    local env_file="$CONSOLE_PATH/.env" port https cert key
    port=$(read_effective_console_setting CONSOLE_PORT "")
    [ -n "$port" ] || port=$(read_effective_console_setting PORT 5000)
    [ "$port" = "0" ] && return 0
    https=$(read_effective_console_setting HTTPS_ENABLED false | tr '[:upper:]' '[:lower:]')
    cert=$(env_file_value "$env_file" SSL_CERT_PATH)
    key=$(env_file_value "$env_file" SSL_KEY_PATH)
    if [ "$https" = "true" ] && [ -f "$cert" ] && [ -f "$key" ]; then
        printf 'https://127.0.0.1:%s/health\n' "$(read_effective_console_setting HTTPS_PORT 5443)"
    else
        printf 'http://127.0.0.1:%s/health\n' "$port"
    fi
}

# The signal and relay ports the server listens on.
signal_port() { read_effective_console_setting SIGNAL_PORT 21116; }
relay_port() { read_effective_console_setting RELAY_PORT 21117; }

# verify_go_install [SECONDS]: the service runs, the API and the web console
# answer /health, signal and relay listen, and the client API port listens
# when it is on.
verify_go_install() {
    local timeout="${1:-60}" waited=0 api_ok=false console_url console_ok=false signal relay
    console_url=$(console_health_url)
    signal=$(signal_port)
    relay=$(relay_port)
    [ -z "$console_url" ] && console_ok=true
    while [ "$waited" -lt "$timeout" ]; do
        if systemctl is-active --quiet "$SERVER_SERVICE"; then
            [ "$api_ok" = true ] || { curl -fsS -o /dev/null --max-time 3 "http://127.0.0.1:${GO_API_PORT:-21114}/api/health" 2>/dev/null && api_ok=true; }
            [ "$console_ok" = true ] || { curl -ksf -o /dev/null --max-time 3 "$console_url" 2>/dev/null && console_ok=true; }
            if [ "$api_ok" = true ] && [ "$console_ok" = true ] && _tcp_port_is_listening "$signal" && _tcp_port_is_listening "$relay"; then
                break
            fi
        fi
        sleep 2
        waited=$((waited + 2))
    done
    if ! systemctl is-active --quiet "$SERVER_SERVICE"; then
        print_error "$SERVER_SERVICE is not running"
        show_service_logs "$SERVER_SERVICE" 30
        return 1
    fi
    [ "$api_ok" = true ] || { print_error "The API does not answer on :${GO_API_PORT:-21114}"; show_service_logs "$SERVER_SERVICE" 30; return 1; }
    [ "$console_ok" = true ] || { print_error "The web console does not answer $console_url"; show_service_logs "$SERVER_SERVICE" 30; return 1; }
    _tcp_port_is_listening "$signal" || { print_error "Signal port $signal is not listening"; return 1; }
    _tcp_port_is_listening "$relay" || { print_error "Relay port $relay is not listening"; return 1; }
    if [ "$(read_effective_console_setting API_ENABLED true | tr '[:upper:]' '[:lower:]')" = "true" ] \
        && [ "$(read_effective_console_setting CONSOLE_PORT 5000)" != "0" ]; then
        local client_port
        client_port=$(read_effective_console_setting API_PORT "${CLIENT_API_PORT:-21121}")
        _tcp_port_is_listening "$client_port" || print_warning "The client API port $client_port is not listening"
    fi
    print_success "betterdesk-server answers: API :${GO_API_PORT:-21114}${console_url:+, web console $console_url}, signal :$signal, relay :$relay"
}

#===============================================================================
# Upgrade of an install with the Node.js console, and its rollback
#===============================================================================

# True when the installed unit runs the server with the web console
# inside: this installer writes BETTERDESK_SUPERVISOR into it.
is_go_only_install() {
    [ -f "$SERVER_UNIT" ] && grep -q '^Environment=BETTERDESK_SUPERVISOR=' "$SERVER_UNIT" 2>/dev/null
}

# True when the install still runs the Node.js console.
is_node_era_install() {
    ! is_go_only_install && { [ -f "$LEGACY_CONSOLE_UNIT" ] || [ -f "$CONSOLE_PATH/server.js" ]; }
}

# Settings of the old units the server does not take over: the console port
# the installer kept off while Node held it, what only Node read, and the
# old binary's path (the updater uses the binary that runs).
UPGRADE_SKIPPED_UNIT_KEYS=" CONSOLE_PORT NODE_EXTRA_CA_CERTS NODE_OPTIONS BETTERDESK_SERVER_BINARY "

# carry_unit_environment UNIT ENV_FILE: appends to the env file the
# settings of a unit's Environment= lines that the file does not set. Under
# the old units a setting in the file won over the unit's (EnvironmentFile=
# overrides Environment=); the server reads the file only for what its own
# unit leaves unset, so the old units' other settings move into the file.
carry_unit_environment() {
    local unit="$1" env_file="$2" pair key header=false
    [ -f "$unit" ] || return 0
    while IFS= read -r pair; do
        key="${pair%%=*}"
        [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
        case "$UPGRADE_SKIPPED_UNIT_KEYS" in *" $key "*) continue ;; esac
        grep -q "^${key}=" "$env_file" 2>/dev/null && continue
        if [ "$header" = false ]; then
            [ -s "$env_file" ] && [ -n "$(tail -c1 "$env_file")" ] && echo >> "$env_file"
            printf '\n# Carried from %s by betterdesk.sh (upgrade to the server with its console)\n' "$(basename "$unit")" >> "$env_file"
            header=true
        fi
        printf '%s\n' "$pair" >> "$env_file"
        print_info "  carry    $key (from $(basename "$unit"))"
    done < <(unit_environment "$unit")
}

# json_string TEXT: TEXT as a JSON string.
json_string() {
    printf '"%s"' "$(printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g')"
}

# record_field FILE NAME: a string field of the upgrade record.
record_field() {
    sed -n "s/.*\"$2\":[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" "$1" 2>/dev/null | head -1
}

# upgrade_backup DIR: the binaries, units, .env, sudoers rule, root helper
# and ownership of an install with the Node.js console, before the upgrade.
upgrade_backup() {
    local dir="$1" bin old_bin
    mkdir -p "$dir/units" "$dir/bin"
    chmod 700 "$dir"
    cp -a "$SERVER_UNIT" "$dir/units/"
    [ -f "$LEGACY_CONSOLE_UNIT" ] && cp -a "$LEGACY_CONSOLE_UNIT" "$dir/units/"
    [ -d "$SERVER_UNIT.d" ] && cp -a "$SERVER_UNIT.d" "$dir/units/"
    [ -d "$LEGACY_CONSOLE_UNIT.d" ] && cp -a "$LEGACY_CONSOLE_UNIT.d" "$dir/units/"
    cp -a "$CONSOLE_PATH/.env" "$dir/console.env"
    [ -f "$LEGACY_SUDOERS_FILE" ] && cp -a "$LEGACY_SUDOERS_FILE" "$dir/sudoers-betterdesk-console-updates"
    [ -d "$LEGACY_BROKER_DIR" ] && cp -a "$LEGACY_BROKER_DIR" "$dir/libexec-betterdesk"

    # Every copy of the old binary the old unit or the folder holds
    old_bin=$(unit_exec_binary "$SERVER_UNIT")
    : > "$dir/bin/paths"
    local n=0
    for bin in "$old_bin" "$(server_binary_path)"; do
        [ -f "$bin" ] || continue
        grep -q -F -x -- "$bin" <(cut -f2 "$dir/bin/paths") && continue
        n=$((n + 1))
        cp -a "$bin" "$dir/bin/$n"
        printf '%s\t%s\n' "$n" "$bin" >> "$dir/bin/paths"
    done

    # Owner and mode of what the upgrade hands to the service account
    {
        find "$RUSTDESK_PATH" -path "$RUSTDESK_PATH/source" -prune -o -printf '%u:%g\t%m\t%p\n'
        find "$CONSOLE_PATH" -maxdepth 2 -path "$CONSOLE_PATH/node_modules" -prune -o -printf '%u:%g\t%m\t%p\n'
    } > "$dir/ownership.tsv" 2>/dev/null || true

    {
        echo "from=$(timeout 10 "${old_bin:-$(server_binary_path)}" -version 2>/dev/null || echo unknown)"
        echo "rustdesk_path=$RUSTDESK_PATH"
        echo "console_path=$CONSOLE_PATH"
        echo "db_type=$(env_file_value "$CONSOLE_PATH/.env" DB_TYPE)"
        echo "at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    } > "$dir/manifest.txt"
}

# upgrade_snapshot_database DIR: a copy of the database while the services
# are stopped: SQLite with .backup (and auth.db), PostgreSQL with pg_dump
# when it is installed.
upgrade_snapshot_database() {
    local dir="$1" db_type db_url
    db_type=$(env_file_value "$CONSOLE_PATH/.env" DB_TYPE)
    if [ "$db_type" = "postgres" ]; then
        db_url=$(env_file_value "$CONSOLE_PATH/.env" DATABASE_URL)
        if command -v pg_dump &>/dev/null && [ -n "$db_url" ]; then
            if pg_dump -Fc -f "$dir/postgres.dump" "$db_url" 2>"$dir/pg_dump.log"; then
                print_info "  PostgreSQL snapshot: $dir/postgres.dump"
            else
                print_warning "pg_dump failed (see $dir/pg_dump.log); there is no database snapshot"
            fi
        else
            print_warning "pg_dump is not installed: there is no database snapshot"
        fi
    elif [ -f "$DB_PATH" ]; then
        if command -v sqlite3 &>/dev/null; then
            sqlite3 "$DB_PATH" ".backup '$dir/db_v2.sqlite3'"
        else
            cp -a "$DB_PATH" "$dir/db_v2.sqlite3"
        fi
        print_info "  SQLite snapshot: $dir/db_v2.sqlite3"
    fi
    if [ -f "$CONSOLE_PATH/data/auth.db" ]; then
        if command -v sqlite3 &>/dev/null; then
            sqlite3 "$CONSOLE_PATH/data/auth.db" ".backup '$dir/auth.db'"
        else
            cp -a "$CONSOLE_PATH/data/auth.db" "$dir/auth.db"
        fi
    fi
}

# upgrade_node_install_to_go: an install with the Node.js console becomes
# one service, betterdesk-server, that serves the web console itself. Data
# stays where it is; the old binary, units and .env are kept, and the
# console's unit stays disabled for one release, so --rollback-go can put
# the old pair back. A failed start rolls back by itself.
upgrade_node_install_to_go() {
    echo -e "${WHITE}${BOLD}══════════ UPGRADE: WEB CONSOLE INTO THE SERVER ══════════${NC}"
    echo ""
    if [ ! -f "$SERVER_UNIT" ] || [ ! -f "$CONSOLE_PATH/.env" ]; then
        print_error "No betterdesk-server.service or $CONSOLE_PATH/.env: this is no install to upgrade"
        return 1
    fi
    print_info "This upgrade moves the web console into betterdesk-server:"
    print_info "  - one service, betterdesk-server, running as $SERVICE_USER, serves the panel on the same ports"
    print_info "  - betterdesk-console is stopped and disabled (kept for a rollback until the next update)"
    print_info "  - data, keys and accounts stay where they are"
    print_info "  - roll back with: sudo ./betterdesk.sh --rollback-go"
    if [ "$AUTO_MODE" != true ] && [ "$UPDATE_MODE" != true ] && ! confirm "Upgrade now?"; then
        return 0
    fi

    # 1. Preflight: room for the binaries and a database copy
    local need_kb free_kb db_kb=0
    [ -f "$DB_PATH" ] && db_kb=$(du -k "$DB_PATH" | cut -f1)
    need_kb=$((2 * 60 * 1024 + db_kb))
    mkdir -p "$BACKUP_DIR"
    free_kb=$(df -Pk "$BACKUP_DIR" | awk 'NR==2 { print $4 }')
    if [ -n "$free_kb" ] && [ "$free_kb" -lt "$need_kb" ]; then
        print_error "Not enough free space in $BACKUP_DIR: ${free_kb} KB free, about ${need_kb} KB needed"
        return 1
    fi

    # 2. The new binary, before anything stops
    print_step "[1/9] Getting the new server binary..."
    obtain_server_binary || { rm -rf "${STAGED_SERVER_DIR:-}"; return 1; }

    # 3. Backup
    local ts backup
    ts=$(date +%Y%m%d-%H%M%S)
    backup="$BACKUP_DIR/pre-go-$ts"
    print_step "[2/9] Backing up binaries, services and .env to $backup..."
    upgrade_backup "$backup" || { print_error "Backup failed; nothing was changed"; rm -rf "$STAGED_SERVER_DIR"; return 1; }

    # 4. Stop both, then the database copy
    print_step "[3/9] Stopping betterdesk-console and betterdesk-server..."
    systemctl stop "$LEGACY_CONSOLE_SERVICE" 2>/dev/null || true
    systemctl stop "$SERVER_SERVICE" 2>/dev/null || true
    wait_for_service_stop "$LEGACY_CONSOLE_SERVICE" 15 || true
    wait_for_service_stop "$SERVER_SERVICE" 15 || true
    upgrade_snapshot_database "$backup"

    # 5. The console's unit stays, disabled
    print_step "[4/9] Disabling betterdesk-console (the unit stays for a rollback)..."
    systemctl disable "$LEGACY_CONSOLE_SERVICE" 2>/dev/null || true

    # 6. The binary; the old one stays as betterdesk-server.pre-go
    print_step "[5/9] Installing the new binary..."
    local old_args db_flag init_pass
    old_args=$(unit_exec_args "$SERVER_UNIT")
    db_flag=$(unit_exec_flag_value "$SERVER_UNIT" -db)
    init_pass=$(unit_exec_flag_value "$SERVER_UNIT" -init-admin-pass)
    if ! install_staged_binary .pre-go; then
        rm -rf "$STAGED_SERVER_DIR"
        rollback_go_upgrade "$backup"
        return 1
    fi
    rm -rf "$STAGED_SERVER_DIR"

    # 7. The env file: the old units' settings it lacks, the database and
    # the first admin password out of ExecStart, then upgrade-env
    print_step "[6/9] Moving the settings into $CONSOLE_PATH/.env..."
    local env_file="$CONSOLE_PATH/.env"
    [ -f "$env_file.pre-go" ] || cp -p "$env_file" "$env_file.pre-go"
    carry_unit_environment "$LEGACY_CONSOLE_UNIT" "$env_file"
    carry_unit_environment "$SERVER_UNIT" "$env_file"
    save_given_update_channel
    if [ -n "$db_flag" ] && ! grep -q '^DB_URL=' "$env_file"; then
        printf 'DB_URL=%s\n' "$db_flag" >> "$env_file"
        print_info "  move     -db -> DB_URL"
    fi
    if [ -n "$init_pass" ] && ! grep -q '^INIT_ADMIN_PASS=' "$env_file"; then
        printf 'INIT_ADMIN_PASS=%s\n' "$init_pass" >> "$env_file"
        print_info "  move     -init-admin-pass -> INIT_ADMIN_PASS"
    fi
    local upgrade_output
    if ! upgrade_output=$(BETTERDESK_ENV_FILE='' "$(server_binary_path)" upgrade-env --from "$env_file" --write --supervisor systemd 2>&1); then
        printf '%s\n' "$upgrade_output"
        print_error "upgrade-env failed; rolling back"
        rollback_go_upgrade "$backup"
        return 1
    fi
    # The plan without the keys it keeps as they are
    printf '%s\n' "$upgrade_output" | grep -v '^  keep ' || true

    # 8. Ownership: the server runs as the service account now
    print_step "[7/9] Handing the server's files to $SERVICE_USER..."
    ensure_service_user
    set_server_folder_permissions
    own_by_service_user "$CONSOLE_PATH"
    chmod 600 "$env_file" "$env_file.pre-go"

    # 9. The one unit, without the sudoers rule and root helper the Node
    # console used (both are in the backup)
    print_step "[8/9] Writing betterdesk-server.service..."
    local extra_env=""
    [ -d "$SERVER_UNIT.d" ] && print_info "  The drop-ins in $SERVER_UNIT.d stay in effect"
    write_server_unit "$old_args" "$extra_env"
    rm -f "$LEGACY_SUDOERS_FILE"
    rm -rf "$LEGACY_BROKER_DIR"
    systemctl daemon-reload
    systemctl enable "$SERVER_SERVICE" 2>/dev/null || true

    # 10. Start and check; roll back when it does not come up
    print_step "[9/9] Starting betterdesk-server..."
    systemctl reset-failed "$SERVER_SERVICE" 2>/dev/null || true
    systemctl start "$SERVER_SERVICE" || true
    if ! verify_go_install 90; then
        print_error "The server with its web console did not come up; putting the old services back"
        rollback_go_upgrade "$backup"
        return 1
    fi

    {
        printf '{"from":%s,' "$(json_string "$(sed -n 's/^from=//p' "$backup/manifest.txt")")"
        printf '"to":%s,' "$(json_string "${STAGED_SERVER_VERSION:-}")"
        printf '"at":%s,' "$(json_string "$(date -u +%Y-%m-%dT%H:%M:%SZ)")"
        printf '"backup":%s}\n' "$(json_string "$backup")"
    } > "$CONSOLE_PATH/data/$GO_UPGRADE_RECORD"
    chown "$SERVICE_USER:$SERVICE_USER" "$CONSOLE_PATH/data/$GO_UPGRADE_RECORD"

    echo ""
    print_success "Upgrade done: betterdesk-server serves the web console"
    print_info "Backup: $backup"
    print_info "Roll back: sudo ./betterdesk.sh --rollback-go"
    print_info "The next successful update removes the Node.js console's code (or: sudo ./betterdesk.sh --cleanup-node)"
}

# rollback_go_upgrade [BACKUP]: puts the binaries, units, .env, sudoers rule,
# root helper and file ownership from before the upgrade back and starts
# betterdesk-server and betterdesk-console. The database stays as it is (the
# server keeps its schema readable by the old version); an interactive run
# offers the snapshot from before the upgrade.
rollback_go_upgrade() {
    local backup="${1:-}" record="$CONSOLE_PATH/data/$GO_UPGRADE_RECORD"
    if [ -z "$backup" ] && [ -f "$record" ]; then
        backup=$(record_field "$record" backup)
    fi
    if [ -z "$backup" ]; then
        backup=$(find "$BACKUP_DIR" -maxdepth 1 -type d -name 'pre-go-*' 2>/dev/null | sort | tail -1)
    fi
    if [ -z "$backup" ] || [ ! -f "$backup/units/betterdesk-server.service" ]; then
        print_error "No backup from an upgrade to roll back to (looked for $BACKUP_DIR/pre-go-*)"
        return 1
    fi
    if [ -f "$backup/units/betterdesk-console.service" ] && [ ! -f "$CONSOLE_PATH/server.js" ]; then
        print_error "The Node.js console's code is gone from $CONSOLE_PATH (--cleanup-node); it cannot be started again"
        return 1
    fi
    print_step "Rolling back to the services from before the upgrade ($backup)..."

    systemctl stop "$SERVER_SERVICE" 2>/dev/null || true
    wait_for_service_stop "$SERVER_SERVICE" 15 || true

    local n path owner mode
    while IFS=$'\t' read -r n path; do
        [ -n "$n" ] && [ -f "$backup/bin/$n" ] && cp -a "$backup/bin/$n" "$path"
    done < "$backup/bin/paths"
    rm -f "$(server_binary_path).pre-go"

    cp -a "$backup/units/betterdesk-server.service" "$SERVER_UNIT"
    if [ -f "$backup/units/betterdesk-console.service" ]; then
        cp -a "$backup/units/betterdesk-console.service" "$LEGACY_CONSOLE_UNIT"
    fi
    rm -rf "$SERVER_UNIT.d"
    [ -d "$backup/units/betterdesk-server.service.d" ] && cp -a "$backup/units/betterdesk-server.service.d" "$SERVER_UNIT.d"

    cp -a "$backup/console.env" "$CONSOLE_PATH/.env"
    rm -f "$CONSOLE_PATH/.env.pre-go"
    if [ -f "$backup/sudoers-betterdesk-console-updates" ]; then
        cp -a "$backup/sudoers-betterdesk-console-updates" "$LEGACY_SUDOERS_FILE"
    fi
    if [ -d "$backup/libexec-betterdesk" ]; then
        rm -rf "$LEGACY_BROKER_DIR"
        cp -a "$backup/libexec-betterdesk" "$LEGACY_BROKER_DIR"
    fi
    while IFS=$'\t' read -r owner mode path; do
        [ -e "$path" ] || continue
        chown -h "$owner" "$path" 2>/dev/null || true
        [ -L "$path" ] || chmod "$mode" "$path" 2>/dev/null || true
    done < "$backup/ownership.tsv"

    if [ "$AUTO_MODE" != true ] && [ "$ROLLBACK_GO_MODE" != true ] \
        && { [ -f "$backup/db_v2.sqlite3" ] || [ -f "$backup/postgres.dump" ]; } \
        && confirm "Also restore the database from before the upgrade? What was saved since is lost."; then
        if [ -f "$backup/db_v2.sqlite3" ]; then
            rm -f "$DB_PATH-wal" "$DB_PATH-shm"
            cp -a "$backup/db_v2.sqlite3" "$DB_PATH"
            [ -f "$backup/auth.db" ] && cp -a "$backup/auth.db" "$CONSOLE_PATH/data/auth.db"
        else
            pg_restore --clean --if-exists -d "$(env_file_value "$CONSOLE_PATH/.env" DATABASE_URL)" "$backup/postgres.dump" \
                || print_warning "pg_restore reported errors"
        fi
        print_info "Database restored from $backup"
    fi

    systemctl daemon-reload
    systemctl reset-failed "$SERVER_SERVICE" "$LEGACY_CONSOLE_SERVICE" 2>/dev/null || true
    systemctl enable "$SERVER_SERVICE" "$LEGACY_CONSOLE_SERVICE" 2>/dev/null || true
    systemctl start "$SERVER_SERVICE" || true
    systemctl start "$LEGACY_CONSOLE_SERVICE" || true
    [ -f "$record" ] && mv -f "$record" "$CONSOLE_PATH/data/.go-upgrade.rolled-back.json"

    if verify_service_health "$SERVER_SERVICE" 21116 60 && verify_service_health "$LEGACY_CONSOLE_SERVICE" "$(resolve_panel_health_port)" 60; then
        print_success "Rolled back: betterdesk-server and betterdesk-console run as before the upgrade"
        return 0
    fi
    print_error "Rolled back, but a service did not come up; see the logs above"
    return 1
}

# cleanup_node_leftovers: deletes the Node.js console's code from the console
# folder and its disabled unit after an upgrade. The console's .env, data
# and its language files, themes and images stay.
cleanup_node_leftovers() {
    if ! is_go_only_install; then
        print_error "This install still runs the Node.js console; upgrade it first (--update)"
        return 1
    fi
    print_step "Removing the Node.js console's code and its disabled service..."
    local item removed=0
    for item in server.js routes services lib middleware scripts node_modules web-ui vendor tests \
                package.json package-lock.json .env.example README.md reset-password.js test-db.js; do
        if [ -e "$CONSOLE_PATH/$item" ]; then
            rm -rf "${CONSOLE_PATH:?}/$item"
            removed=$((removed + 1))
        fi
    done
    if [ -f "$LEGACY_CONSOLE_UNIT" ]; then
        systemctl disable --now "$LEGACY_CONSOLE_SERVICE" 2>/dev/null || true
        rm -f "$LEGACY_CONSOLE_UNIT"
        rm -rf "$LEGACY_CONSOLE_UNIT.d"
        systemctl daemon-reload
        removed=$((removed + 1))
    fi
    rm -f "$(server_binary_path).pre-go"
    print_success "Removed $removed leftovers of the Node.js console"
    if command -v node &>/dev/null; then
        print_info "BetterDesk no longer needs Node.js; remove it with your package manager if nothing else uses it"
    fi
}

# update_go_install: the newest release of the update channel (or --binary,
# --from-source, RELEASE_BASE_URL), the unit written again, a restart and a
# check. A new binary that does not come up is replaced by the previous one.
update_go_install() {
    local bin before after
    bin=$(server_binary_path)
    before=$(sha256sum "$bin" 2>/dev/null | awk '{ print $1 }')
    print_info "Creating backup before update..."
    do_backup_silent
    # The server's updater reads the channel from .env
    save_given_update_channel

    if [ -n "$SERVER_BINARY_FILE" ] || [ "$FROM_SOURCE" = true ] || [ -n "${RELEASE_BASE_URL:-}" ]; then
        obtain_server_binary || { rm -rf "${STAGED_SERVER_DIR:-}"; return 1; }
        systemctl stop "$SERVER_SERVICE" 2>/dev/null || true
        wait_for_service_stop "$SERVER_SERVICE" 15 || true
        install_staged_binary .prev || { rm -rf "$STAGED_SERVER_DIR"; systemctl start "$SERVER_SERVICE" || true; return 1; }
        rm -rf "$STAGED_SERVER_DIR"
    else
        # The server's own updater: the release of the channel, its manifest
        # signature and SHA-256 checked, the old binary kept as .prev
        print_step "Installing the newest release with betterdesk-server update..."
        if ! server_cmd --as-service update --yes; then
            print_error "betterdesk-server update failed"
            return 1
        fi
    fi
    after=$(sha256sum "$bin" 2>/dev/null | awk '{ print $1 }')

    merge_console_env false || print_warning ".env merge reported a problem"
    maybe_update_services
    set_server_folder_permissions
    systemctl reset-failed "$SERVER_SERVICE" 2>/dev/null || true
    systemctl restart "$SERVER_SERVICE" || true
    if ! verify_go_install 90; then
        if [ "$before" != "$after" ] && [ -f "$bin.prev" ]; then
            print_error "The new binary did not come up; putting the previous one back"
            systemctl stop "$SERVER_SERVICE" 2>/dev/null || true
            cp -a "$bin.prev" "$bin"
            systemctl start "$SERVER_SERVICE" || true
            verify_go_install 60 || true
        fi
        return 1
    fi

    # One release after the upgrade from the Node.js console, its code goes
    if [ "$before" != "$after" ] && [ -f "$CONSOLE_PATH/data/$GO_UPGRADE_RECORD" ] \
        && { [ -f "$LEGACY_CONSOLE_UNIT" ] || [ -f "$CONSOLE_PATH/server.js" ]; }; then
        cleanup_node_leftovers || true
    fi
    if [ "$before" = "$after" ]; then
        print_success "betterdesk-server is up to date: $(timeout 10 "$bin" -version 2>/dev/null)"
    else
        print_success "Updated to $(timeout 10 "$bin" -version 2>/dev/null)"
    fi
    install_manager_command
}

# Shows the admin's first password after a fresh install.
create_admin_user() {
    print_step "Admin account..."

    # The server makes the admin at its first start from
    # DEFAULT_ADMIN_PASSWORD. Prefer the installer's password, then .env.
    local admin_password="${ADMIN_PASSWORD:-}"
    if [ -z "$admin_password" ]; then
        admin_password=$(env_file_value "$CONSOLE_PATH/.env" DEFAULT_ADMIN_PASSWORD)
    fi

    if [ -n "$admin_password" ]; then
        echo ""
        echo -e "${GREEN}╔════════════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║            PANEL LOGIN CREDENTIALS                    ║${NC}"
        echo -e "${GREEN}╠════════════════════════════════════════════════════════╣${NC}"
        echo -e "${GREEN}║  Login:    ${WHITE}admin${GREEN}                                     ║${NC}"
        echo -e "${GREEN}║  Password: ${WHITE}${admin_password}${GREEN}                         ║${NC}"
        echo -e "${GREEN}╚════════════════════════════════════════════════════════╝${NC}"
        echo ""

        if [ "$STORE_ADMIN_CREDENTIALS" = "true" ]; then
            cat > "$RUSTDESK_PATH/.admin_credentials" << CREDEOF
Admin Username: admin
Admin Password: $admin_password
Generated by: BetterDesk installer
Timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)
CREDEOF
            chown root:root "$RUSTDESK_PATH/.admin_credentials" 2>/dev/null || true
            chmod 600 "$RUSTDESK_PATH/.admin_credentials"
            print_info "Credentials saved in: $RUSTDESK_PATH/.admin_credentials (root-only, 0600)"
        else
            print_warning "Credentials are not persisted by default (security hardening)."
            print_info "Set STORE_ADMIN_CREDENTIALS=true to restore legacy behavior."
        fi
    else
        print_warning "No admin password available for display"
        print_info "Use option 6 (Password reset) if needed"
    fi
}

start_services() {
    start_services_with_verification
}

#===============================================================================
# Installation
#===============================================================================

# Minimal installation: the same server with its web console off
# (CONSOLE_PORT=0 in .env); setting a port there turns it on.
do_install_minimal() {
    MINIMAL_MODE=true
    do_install
}

do_install() {
    print_header
    if [ "$MINIMAL_MODE" = true ]; then
        echo -e "${WHITE}${BOLD}══════════ MINIMAL INSTALLATION (web console off) ══════════${NC}"
    else
        echo -e "${WHITE}${BOLD}══════════ FRESH INSTALLATION ══════════${NC}"
    fi
    echo ""

    detect_installation

    # An install with the Node.js console is upgraded in place, never
    # installed over
    if is_node_era_install; then
        print_warning "This server runs the Node.js web console."
        print_info "The upgrade moves it into betterdesk-server and keeps all data."
        upgrade_node_install_to_go
        return
    fi

    if [ "$INSTALL_STATUS" = "complete" ]; then
        print_warning "BetterDesk is already installed!"
        if [ "$AUTO_MODE" = false ]; then
            if ! confirm "Do you want to reinstall?"; then
                return
            fi
        fi
        do_backup_silent
    fi

    echo ""
    print_info "Starting BetterDesk v$VERSION installation..."
    echo ""

    # Choose database type (SQLite or PostgreSQL)
    choose_database_type

    # Stop services if running (prevents "Text file busy" error)
    graceful_stop_services

    install_dependencies

    # Install and configure PostgreSQL if selected
    if [ "$USE_POSTGRESQL" = "true" ]; then
        install_postgresql || { print_error "PostgreSQL installation failed"; return 1; }
        setup_postgresql_database || { print_error "PostgreSQL setup failed"; return 1; }
    fi

    detect_architecture
    install_binaries || { print_error "Binary installation failed"; return 1; }
    setup_console_home || { print_error "Setting up the console folder failed"; return 1; }

    # Generate self-signed TLS certificates (default for fresh installs)
    generate_ssl_certificates

    # Migrate existing SQLite data to PostgreSQL if applicable
    if [ "$USE_POSTGRESQL" = "true" ]; then
        migrate_sqlite_to_postgresql
    fi

    setup_services
    create_admin_user

    # Configure firewall rules
    print_step "Configuring firewall rules..."
    configure_firewall_rules

    local install_ok=true
    if ! start_services; then
        install_ok=false
    fi

    echo ""
    if [ "$install_ok" = true ]; then
        print_success "Installation completed successfully!"
    else
        print_warning "Installation finished but betterdesk-server does not answer."
        print_info "Run option 8 (Diagnostics) to investigate."
    fi
    echo ""

    local server_ip
    server_ip=$(get_public_ip)
    local public_key=""
    if [ -f "$RUSTDESK_PATH/id_ed25519.pub" ]; then
        public_key=$(cat "$RUSTDESK_PATH/id_ed25519.pub")
    fi

    local db_type_info="SQLite"
    if [ "$USE_POSTGRESQL" = "true" ]; then
        db_type_info="PostgreSQL"
    fi

    local tls_status="Disabled"
    if [ -f "$RUSTDESK_PATH/ssl/betterdesk.crt" ] && [ -f "$RUSTDESK_PATH/ssl/betterdesk.key" ]; then
        tls_status="Self-signed (auto-generated)"
    fi

    local panel_url="off (minimal installation)"
    if [ "$MINIMAL_MODE" != true ]; then
        if [ "$(read_effective_console_setting HTTPS_ENABLED false | tr '[:upper:]' '[:lower:]')" = "true" ]; then
            panel_url="https://$server_ip:$(resolve_panel_https_port)"
        else
            panel_url="http://$server_ip:$(resolve_panel_http_port)"
        fi
    fi

    echo -e "${CYAN}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${CYAN}║              INSTALLATION INFO                             ║${NC}"
    echo -e "${CYAN}╠════════════════════════════════════════════════════════════╣${NC}"
    echo -e "${CYAN}║  Panel Web:     ${WHITE}$panel_url${CYAN}                        ║${NC}"
    echo -e "${CYAN}║  API Port:      ${WHITE}$API_PORT${CYAN}                                     ║${NC}"
    echo -e "${CYAN}║  Server ID:     ${WHITE}$server_ip${CYAN}                                    ║${NC}"
    echo -e "${CYAN}║  Database:      ${WHITE}$db_type_info${CYAN}                                 ║${NC}"
    echo -e "${CYAN}║  TLS:           ${WHITE}$tls_status${CYAN}                                   ║${NC}"
    echo -e "${CYAN}║  Key:           ${WHITE}${public_key:0:20}...${CYAN}                          ║${NC}"
    echo -e "${CYAN}╚════════════════════════════════════════════════════════════╝${NC}"

    # Offer TLS configuration for fresh installs
    if [ "$install_ok" = true ] && [ "$AUTO_MODE" = false ] && [ "$MINIMAL_MODE" != true ]; then
        echo ""
        print_info "Production TLS options:"
        print_info "  • External reverse proxy (Caddy/Nginx on :443) — recommended when a proxy already handles certificates"
        print_info "  • Enterprise TLS (Option 5 in SSL menu) — BetterDesk-native HTTPS on panel + signal/relay"
        echo ""
        if confirm "Will TLS terminate at an external reverse proxy (Caddy/Nginx)?"; then
            do_configure_reverse_proxy || true
        elif confirm "Would you like to configure HTTPS Enterprise now? (Option 5 in SSL menu)"; then
            do_configure_ssl
        fi
    fi

    [ "$install_ok" = true ] && install_manager_command

    if [ "$AUTO_MODE" = false ]; then
        press_enter
    fi
    [ "$install_ok" = true ]
}

#===============================================================================
# Update Functions
#===============================================================================

# GitHub repository configuration for online updates
UPDATE_GITHUB_OWNER="${UPDATE_GITHUB_OWNER:-trybetterdesk}"
UPDATE_GITHUB_REPO="${UPDATE_GITHUB_REPO:-betterdesk}"
# A channel given to the installer (install.sh passes the branch it
# installed) wins over .env and is written into it
UPDATE_GITHUB_BRANCH_GIVEN="${UPDATE_GITHUB_BRANCH:-}"
UPDATE_GITHUB_BRANCH="${UPDATE_GITHUB_BRANCH:-main}"

# save_given_update_channel: the channel the installer was given goes into
# .env, where the server's updater reads it
save_given_update_channel() {
    if [ -n "${UPDATE_GITHUB_BRANCH_GIVEN:-}" ] && [ -f "${CONSOLE_PATH:-}/.env" ]; then
        _upsert_env_line "$CONSOLE_PATH/.env" UPDATE_GITHUB_BRANCH "$UPDATE_GITHUB_BRANCH_GIVEN"
    fi
    return 0
}

read_update_github_branch_from_env() {
    local env_file="${CONSOLE_PATH:-}/.env"
    [ -n "${UPDATE_GITHUB_BRANCH_GIVEN:-}" ] && return 0
    if [ -n "$CONSOLE_PATH" ] && [ -f "$env_file" ]; then
        local val
        val=$(grep -E '^UPDATE_GITHUB_BRANCH=' "$env_file" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d '\r"')
        if [ -n "$val" ]; then
            UPDATE_GITHUB_BRANCH="$val"
            export UPDATE_GITHUB_BRANCH
        fi
    fi
}

write_update_github_branch_to_env() {
    local branch="$1"
    local env_file="${CONSOLE_PATH:-}/.env"
    if [ -z "$CONSOLE_PATH" ]; then
        print_error "Console path unknown — cannot save update channel"
        return 1
    fi
    if [ ! -f "$env_file" ]; then
        touch "$env_file"
    fi
    if grep -qE '^UPDATE_GITHUB_BRANCH=' "$env_file" 2>/dev/null; then
        sed -i "s/^UPDATE_GITHUB_BRANCH=.*/UPDATE_GITHUB_BRANCH=${branch}/" "$env_file"
    else
        printf '\nUPDATE_GITHUB_BRANCH=%s\n' "$branch" >> "$env_file"
    fi
    UPDATE_GITHUB_BRANCH="$branch"
    UPDATE_GITHUB_BRANCH_GIVEN="${UPDATE_GITHUB_BRANCH_GIVEN:+$branch}"
    export UPDATE_GITHUB_BRANCH
    print_success "Update channel saved (GitHub branch: $branch)"
    return 0
}

switch_update_channel() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ UPDATE CHANNEL ══════════${NC}"
    echo ""
    detect_installation
    if [ "$INSTALL_STATUS" = "none" ]; then
        print_error "BetterDesk is not installed!"
        press_enter
        return
    fi
    read_update_github_branch_from_env
    print_info "Current GitHub branch: $UPDATE_GITHUB_BRANCH"
    echo ""
    local _menu_items=(
        $'Stable (main)\tProduction releases from the main branch'
        $'Development (dev)\tLatest work-in-progress from the dev branch'
        $'Back\tReturn without changes'
    )
    local _menu_returns=( main dev 0 )
    menu_choose "Update Channel" "Stable is recommended for production servers"
    case "${MENU_CHOICE:-main}" in
        0) return ;;
        main)
            write_update_github_branch_to_env "main"
            ;;
        dev)
            print_warning "Development channel may include unstable changes."
            write_update_github_branch_to_env "dev"
            ;;
        *)
            write_update_github_branch_to_env "main"
            ;;
    esac
    print_info "The next update (Settings > Updates in the panel, or Update here) installs the channel's release."
    press_enter
}

# If Update already wrote a newer betterdesk.sh, re-exec before Repair/Toggle (#219).
maybe_reexec_if_installer_on_disk_is_newer() {
    if [ "${AUTO_MODE:-false}" = "true" ]; then
        return 0
    fi
    if [ "${BETTERDESK_REEXECED:-}" = "1" ]; then
        return 0
    fi
    local self="${SCRIPT_DIR}/betterdesk.sh"
    [ -f "$self" ] || return 0
    local disk_rev
    disk_rev=$(grep -m1 '^BETTERDESK_SH_REVISION=' "$self" 2>/dev/null | cut -d= -f2- | tr -d "\"'[:space:]")
    if [ -z "$disk_rev" ] || [ "$disk_rev" = "${BETTERDESK_SH_REVISION:-}" ]; then
        return 0
    fi
    print_info "Installer on disk is newer (revision $disk_rev) — reloading before this action (#219)"
    exec env BETTERDESK_REEXECED=1 bash "$self" "${BETTERDESK_ORIG_ARGV[@]}"
}

do_update() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ UPDATE ══════════${NC}"
    echo ""

    detect_installation

    if [ "$INSTALL_STATUS" = "none" ] && ! is_node_era_install; then
        print_error "BetterDesk is not installed!"
        print_info "Use 'FRESH INSTALLATION' option"
        [ "$AUTO_MODE" = true ] || press_enter
        return 1
    fi

    # Detect Rust → Go upgrade (major architecture change)
    if [ "${SERVER_TYPE:-}" = "rust" ]; then
        print_warning "Legacy Rust server (hbbs/hbbr) detected!"
        print_warning "Upgrading from Rust to Go server requires a FRESH INSTALLATION."
        print_info "The Go server is a single binary replacing both hbbs and hbbr."
        print_info "Your data (keys, database) will be preserved during migration."
        echo ""
        if [ "${AUTO_MODE:-false}" = "true" ]; then
            print_info "Auto mode: Redirecting to fresh installation for Rust → Go migration"
            do_install
            return
        else
            read -rp "Proceed with fresh installation (recommended)? [Y/n] " answer
            if [ "${answer,,}" != "n" ]; then
                do_install
                return
            fi
            return 0
        fi
    fi

    # CRITICAL: Preserve database configuration before the .env is merged
    # This prevents PostgreSQL → SQLite switch during updates
    preserve_database_config
    read_update_github_branch_from_env

    # The Node.js console moves into the server: one upgrade, in place
    if is_node_era_install; then
        local rc=0
        upgrade_node_install_to_go || rc=$?
        [ "$AUTO_MODE" = true ] || [ "$UPDATE_MODE" = true ] || press_enter
        return "$rc"
    fi

    print_info "Update channel: $UPDATE_GITHUB_BRANCH ($( [ "$UPDATE_GITHUB_BRANCH" = dev ] && echo development || echo stable))"
    if [ "${AUTO_MODE:-false}" != "true" ] && [ "$UPDATE_MODE" != true ]; then
        local _menu_items=(
            $'Update now\tInstall the newest release of the update channel'
            $'Switch update channel\tChoose stable (main) or development (dev)'
            $'Back\tReturn to the main menu'
        )
        local _menu_returns=( 1 2 0 )
        menu_choose "Update" "Releases are checked by SHA-256 before they replace the binary"
        case "${MENU_CHOICE:-1}" in
            0) return 0 ;;
            2) switch_update_channel; return 0 ;;
        esac
    fi

    local rc=0
    update_go_install || rc=$?
    if [ "$rc" -ne 0 ]; then
        print_error "Update failed"
    fi
    [ "$AUTO_MODE" = true ] || [ "$UPDATE_MODE" = true ] || press_enter
    return "$rc"
}

#===============================================================================
# Repair Functions
#===============================================================================

do_repair() {
    maybe_reexec_if_installer_on_disk_is_newer
    print_header
    echo -e "${WHITE}${BOLD}══════════ REPAIR INSTALLATION ══════════${NC}"
    echo ""
    
    detect_installation
    check_management_capabilities || print_warning "Repair will report permission blockers before changing protected files"
    
    # CRITICAL: Preserve database configuration before any repair operation
    # This prevents PostgreSQL → SQLite switch when regenerating service files
    preserve_database_config
    
    print_status
    
    local _menu_items=(
        $'Repair binaries\tReplace the server binary with BetterDesk Go'
        $'Repair database\tAdd missing columns / run migrations'
        $'Repair services\tRegenerate systemd service units'
        $'Repair permissions\tFix file ownership and permissions'
        $'Repair HTTPS / TLS\tFix LE certs, signal port :5000 conflict (#219)'
        $'Full repair\tRun all repair steps above'
        $'Back\tReturn to the main menu'
    )
    local _menu_returns=( 1 2 3 4 5 6 0 )
    menu_choose "Repair Installation" "Choose what to repair"
    local repair_choice="$MENU_CHOICE"
    
    case $repair_choice in
        1) repair_binaries ;;
        2) repair_database ;;
        3) repair_services ;;
        4) repair_permissions ;;
        5) repair_https_tls ;;
        6) 
            repair_binaries
            repair_database
            repair_services
            repair_permissions
            repair_https_tls
            print_success "Full repair completed!"
            ;;
        0) return ;;
    esac
    
    press_enter
}

repair_binaries() {
    print_step "Repairing betterdesk-server..."
    if is_node_era_install; then
        print_error "This install runs the Node.js console; use Update to upgrade it"
        return 1
    fi
    if ! obtain_server_binary; then
        rm -rf "${STAGED_SERVER_DIR:-}"
        print_error "Could not get a server binary"
        return 1
    fi
    graceful_stop_services
    install_staged_binary .prev || { rm -rf "$STAGED_SERVER_DIR"; return 1; }
    rm -rf "$STAGED_SERVER_DIR"
    if ! start_services_with_verification; then
        print_error "betterdesk-server failed to start after the binary repair"
        return 1
    fi
    print_success "Server binary repaired and verified!"
}

# The server migrates its schema at every start; this checks the SQLite
# file's integrity.
repair_database() {
    print_step "Checking the database..."
    if [ "$(env_file_value "$CONSOLE_PATH/.env" DB_TYPE)" = "postgres" ]; then
        print_info "PostgreSQL: betterdesk-server migrates its tables at start"
        return 0
    fi
    if [ ! -f "$DB_PATH" ]; then
        print_warning "No database at $DB_PATH; betterdesk-server creates it at start"
        return 0
    fi
    if ! command -v sqlite3 &>/dev/null; then
        print_warning "sqlite3 is not installed; skipping the check"
        return 0
    fi
    local result
    result=$(sqlite3 "$DB_PATH" "PRAGMA integrity_check;" 2>&1 | head -5)
    if [ "$result" = "ok" ]; then
        print_success "Database integrity: ok (betterdesk-server migrates its tables at start)"
    else
        print_error "Database integrity check: $result"
        return 1
    fi
}

repair_services() {
    print_step "Repairing the systemd service..."

    if is_node_era_install; then
        print_error "This install runs the Node.js console; use Update to upgrade it"
        return 1
    fi
    if [ ! -f "$(server_binary_path)" ]; then
        print_error "betterdesk-server binary not found at $(server_binary_path)"
        print_info "Run 'Repair binaries' first"
        return 1
    fi

    graceful_stop_services
    local backup_file=""
    if [ -f "$SERVER_UNIT" ]; then
        backup_file="$SERVER_UNIT.backup.$(date +%Y%m%d%H%M%S)"
        cp "$SERVER_UNIT" "$backup_file"
    fi

    maybe_update_services

    if ! start_services_with_verification; then
        print_error "betterdesk-server failed to start after the repair"
        if [ -n "$backup_file" ]; then
            print_info "Restoring the previous unit..."
            cp "$backup_file" "$SERVER_UNIT"
            systemctl daemon-reload
            systemctl start "$SERVER_SERVICE" 2>/dev/null || true
        fi
        return 1
    fi

    print_success "Service repaired and verified!"
}

repair_permissions() {
    print_step "Repairing permissions..."
    ensure_service_user || return 1
    if [ -d "$CONSOLE_PATH" ]; then
        own_by_service_user "$CONSOLE_PATH"
        chmod 700 "$CONSOLE_PATH/data" 2>/dev/null || true
        chmod 600 "$CONSOLE_PATH/.env" 2>/dev/null || true
    fi
    set_server_folder_permissions
    repair_https_stuck_state yes
    ensure_console_tls_material_readable 2>/dev/null || true

    if systemctl is-enabled --quiet "$SERVER_SERVICE" 2>/dev/null; then
        systemctl restart "$SERVER_SERVICE" 2>/dev/null || true
        if verify_go_install 60; then
            print_success "Permissions repaired — betterdesk-server is running"
        else
            print_warning "Permissions synced but betterdesk-server does not answer — check: journalctl -u betterdesk-server -n 40 --no-pager"
        fi
    else
        print_success "Permissions repaired"
    fi
}

repair_https_tls() {
    print_step "Repairing HTTPS / TLS configuration (#219)..."

    repair_https_stuck_state

    if confirm "Restart BetterDesk services now?"; then
        systemctl restart "$SERVER_SERVICE" 2>/dev/null || true
        verify_go_install 60 >/dev/null 2>&1 || true
        print_success "betterdesk-server restarted"
        run_protocol_tests
    else
        print_info "Repair saved. Restart later: systemctl restart betterdesk-server"
    fi
}

#===============================================================================
# Validation Functions
#===============================================================================

do_validate() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ INSTALLATION VALIDATION ══════════${NC}"
    echo ""
    
    local errors=0
    local warnings=0
    
    detect_installation
    detect_architecture
    
    echo -e "${WHITE}Checking components...${NC}"
    echo ""
    
    # Check directories
    echo -n "  RustDesk directory ($RUSTDESK_PATH): "
    if [ -d "$RUSTDESK_PATH" ]; then
        echo -e "${GREEN}✓${NC}"
    else
        echo -e "${RED}✗ Not found${NC}"
        errors=$((errors + 1))
    fi
    
    echo -n "  Console directory ($CONSOLE_PATH): "
    if [ -d "$CONSOLE_PATH" ]; then
        echo -e "${GREEN}✓${NC}"
    else
        echo -e "${RED}✗ Not found${NC}"
        errors=$((errors + 1))
    fi
    
    # Check Go server binary
    echo -n "  BetterDesk Server (Go): "
    if [ -x "$RUSTDESK_PATH/betterdesk-server" ]; then
        echo -e "${GREEN}✓ Single binary (signal + relay + API)${NC}"
    elif [ -x "$RUSTDESK_PATH/hbbs" ] && [ -x "$RUSTDESK_PATH/hbbr" ]; then
        echo -e "${YELLOW}! Legacy Rust binaries (consider upgrading to Go)${NC}"
        warnings=$((warnings + 1))
    else
        echo -e "${RED}✗ Not found or missing permissions${NC}"
        errors=$((errors + 1))
    fi
    
    # Check database
    echo -n "  Database: "
    local validate_db_type="sqlite"
    if [ -f "$CONSOLE_PATH/.env" ]; then
        validate_db_type=$(grep -m1 '^DB_TYPE=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')
        validate_db_type="${validate_db_type:-sqlite}"
    fi

    if [ "$validate_db_type" = "postgres" ]; then
        local pg_uri
        pg_uri=$(grep -m1 '^DATABASE_URL=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2-)
        if [ -n "$pg_uri" ] && PGCONNECT_TIMEOUT=3 psql "$pg_uri" -c "SELECT 1" &>/dev/null 2>&1; then
            echo -e "${GREEN}✓ PostgreSQL${NC}"
            # Check tables in PostgreSQL
            echo -n "    - Table peers: "
            if PGCONNECT_TIMEOUT=3 psql "$pg_uri" -c "SELECT 1 FROM peers LIMIT 1" &>/dev/null 2>&1; then
                echo -e "${GREEN}✓${NC}"
            else
                echo -e "${YELLOW}! Empty or not found (will be created on first start)${NC}"
                warnings=$((warnings + 1))
            fi
            echo -n "    - Table users: "
            if PGCONNECT_TIMEOUT=3 psql "$pg_uri" -c "SELECT 1 FROM users LIMIT 1" &>/dev/null 2>&1; then
                echo -e "${GREEN}✓${NC}"
            else
                echo -e "${YELLOW}! Empty or not found (will be created on first start)${NC}"
                warnings=$((warnings + 1))
            fi
        else
            echo -e "${RED}✗ PostgreSQL connection failed${NC}"
            errors=$((errors + 1))
        fi
    elif [ -f "$DB_PATH" ]; then
        echo -e "${GREEN}✓ SQLite${NC}"
        
        # Check tables (Go uses 'peers', legacy uses 'peer')
        echo -n "    - Table peers: "
        if sqlite3 "$DB_PATH" "SELECT 1 FROM peers LIMIT 1" 2>/dev/null; then
            echo -e "${GREEN}✓${NC}"
        elif sqlite3 "$DB_PATH" "SELECT 1 FROM peer LIMIT 1" 2>/dev/null; then
            echo -e "${YELLOW}! Legacy schema (peer)${NC}"
            warnings=$((warnings + 1))
        else
            echo -e "${YELLOW}! Empty or not found (will be created on first start)${NC}"
            warnings=$((warnings + 1))
        fi
        
        echo -n "    - Table users: "
        if sqlite3 "$DB_PATH" "SELECT 1 FROM users LIMIT 1" 2>/dev/null; then
            echo -e "${GREEN}✓${NC}"
        else
            echo -e "${YELLOW}! Empty or not found (will be created on first start)${NC}"
            warnings=$((warnings + 1))
        fi
    else
        # Check if Go server is running — it creates the DB on start
        if systemctl is-active --quiet betterdesk-server 2>/dev/null; then
            echo -e "${YELLOW}! SQLite file not yet created (server running, will create on first connection)${NC}"
            warnings=$((warnings + 1))
        else
            echo -e "${RED}✗ Not found (will be created when server starts)${NC}"
            errors=$((errors + 1))
        fi
    fi
    
    # Check keys
    echo -n "  Ed25519 key: "
    if [ -f "$RUSTDESK_PATH/id_ed25519.pub" ] || [ -f "$RUSTDESK_PATH/id_ed25519" ]; then
        echo -e "${GREEN}✓${NC}"
    else
        echo -e "${YELLOW}! Will be generated on first start${NC}"
        warnings=$((warnings + 1))
    fi
    
    # Check services
    echo ""
    echo -e "${WHITE}Checking services...${NC}"
    echo ""
    
    # Check Go server service first
    echo -n "  betterdesk-server (Go): "
    if systemctl is-active --quiet betterdesk-server 2>/dev/null; then
        echo -e "${GREEN}● Active (signal + relay + API)${NC}"
    elif systemctl is-enabled --quiet betterdesk-server 2>/dev/null; then
        echo -e "${YELLOW}○ Enabled but inactive${NC}"
        warnings=$((warnings + 1))
    elif systemctl list-unit-files betterdesk-server.service &>/dev/null 2>&1; then
        echo -e "${RED}○ Disabled${NC}"
        errors=$((errors + 1))
    else
        # Check legacy Rust services
        echo -e "${CYAN}Not installed${NC}"
        
        for service in rustdesksignal rustdeskrelay; do
            echo -n "  $service (Legacy Rust): "
            if systemctl is-active --quiet "$service" 2>/dev/null; then
                echo -e "${GREEN}● Active${NC}"
            elif systemctl is-enabled --quiet "$service" 2>/dev/null; then
                echo -e "${YELLOW}○ Enabled but inactive${NC}"
                warnings=$((warnings + 1))
            else
                echo -e "${RED}○ Disabled${NC}"
                errors=$((errors + 1))
            fi
        done
    fi
    
    if is_go_only_install; then
        echo -n "  Web console (in betterdesk-server): "
        local console_url
        console_url=$(console_health_url)
        if [ -z "$console_url" ]; then
            echo -e "${CYAN}off (CONSOLE_PORT=0)${NC}"
        elif curl -ksf -o /dev/null --max-time 5 "$console_url" 2>/dev/null; then
            echo -e "${GREEN}● Answers${NC} ($console_url)"
        else
            echo -e "${RED}✗ No answer${NC} ($console_url)"
            errors=$((errors + 1))
        fi
        if [ -f "$LEGACY_CONSOLE_UNIT" ]; then
            echo -e "  betterdesk-console (Node.js): ${CYAN}kept disabled for a rollback${NC} (removed by the next update or --cleanup-node)"
        fi
    else
        echo -n "  betterdesk-console (Node.js): "
        if systemctl is-active --quiet "$LEGACY_CONSOLE_SERVICE" 2>/dev/null; then
            echo -e "${GREEN}● Active${NC} (Update moves the console into betterdesk-server)"
        elif systemctl is-enabled --quiet "$LEGACY_CONSOLE_SERVICE" 2>/dev/null; then
            echo -e "${YELLOW}○ Enabled but inactive${NC}"
            warnings=$((warnings + 1))
        else
            echo -e "${RED}○ Disabled${NC}"
            errors=$((errors + 1))
        fi
    fi

    # Check ports
    echo ""
    echo -e "${WHITE}Checking ports...${NC}"
    echo ""

    for port in "${GO_API_PORT:-21114}" "${CLIENT_API_PORT:-21121}" 21115 21116 21117 "$(resolve_panel_http_port)"; do
        echo -n "  Port $port: "
        if ss -tlnp 2>/dev/null | grep -q ":$port " || netstat -tlnp 2>/dev/null | grep -q ":$port "; then
            local pname=$(ss -tlnp 2>/dev/null | grep ":$port " | grep -oP 'users:\(\("\K[^"]+' 2>/dev/null | head -1)
            echo -e "${GREEN}● Listening${NC}${pname:+ ($pname)}"
        else
            echo -e "${YELLOW}○ Free${NC}"
            warnings=$((warnings + 1))
        fi
    done
    
    # Summary
    echo ""
    echo -e "${WHITE}═══════════════════════════════════════${NC}"
    
    if [ $errors -eq 0 ] && [ $warnings -eq 0 ]; then
        echo -e "${GREEN}✓ Installation correct - no problems found${NC}"
    elif [ $errors -eq 0 ]; then
        echo -e "${YELLOW}! Found $warnings warnings${NC}"
    else
        echo -e "${RED}✗ Found $errors errors and $warnings warnings${NC}"
        echo ""
        echo -e "${CYAN}Use 'REPAIR INSTALLATION' option to fix problems${NC}"
    fi
    
    press_enter
}

#===============================================================================
# Backup Functions
#===============================================================================

do_backup() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ BACKUP ══════════${NC}"
    echo ""
    
    do_backup_silent
    
    print_success "Backup completed!"
    press_enter
}

do_backup_silent() {
    local backup_name="betterdesk_backup_$(date +%Y%m%d_%H%M%S)"
    local backup_path="$BACKUP_DIR/$backup_name"
    
    mkdir -p "$backup_path"
    
    print_step "Creating backup: $backup_name"
    
    # Backup database
    if [ -f "$DB_PATH" ]; then
        cp "$DB_PATH" "$backup_path/"
        print_info "  - Database"
    fi
    
    # Backup keys
    if [ -f "$RUSTDESK_PATH/id_ed25519" ]; then
        cp "$RUSTDESK_PATH/id_ed25519"* "$backup_path/"
        print_info "  - Keys"
    fi
    
    # Backup API key
    if [ -f "$RUSTDESK_PATH/.api_key" ]; then
        cp "$RUSTDESK_PATH/.api_key" "$backup_path/"
        print_info "  - API key"
    fi
    
    # Backup credentials
    if [ -f "$RUSTDESK_PATH/.admin_credentials" ]; then
        cp "$RUSTDESK_PATH/.admin_credentials" "$backup_path/"
        print_info "  - Login credentials"
    fi
    
    # Create archive
    cd "$BACKUP_DIR"
    tar -czf "$backup_name.tar.gz" "$backup_name"
    rm -rf "$backup_name"
    
    print_success "Backup saved: $BACKUP_DIR/$backup_name.tar.gz"
}

#===============================================================================
# Password Reset Functions
#===============================================================================

do_reset_password() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ ADMIN PASSWORD RESET ══════════${NC}"
    echo ""

    # Refresh detection
    auto_detect_paths

    if [ "$CONSOLE_TYPE" = "none" ]; then
        print_error "No console installation detected!"
        press_enter
        return
    fi
    if [ "$CONSOLE_TYPE" = "nodejs" ]; then
        print_error "This install runs the Node.js console; use Update to move it into betterdesk-server first"
        press_enter
        return
    fi

    local _menu_items=(
        $'Generate random password\tCreate a strong random admin password'
        $'Set custom password\tType a new password (min. 8 characters)'
        $'Back\tReturn to the main menu'
    )
    local _menu_returns=( 1 2 0 )
    menu_choose "Admin Password Reset" "The account admin of the web console"
    local pw_choice="$MENU_CHOICE"

    local new_password

    case $pw_choice in
        1)
            # M-05: full hex entropy
            new_password=$(openssl rand -hex 16)
            ;;
        2)
            echo ""
            read -sp "Enter new password (min. 8 characters): " new_password
            echo ""
            if [ ${#new_password} -lt 8 ]; then
                print_error "Password must be at least 8 characters!"
                press_enter
                return
            fi
            ;;
        *)
            return
            ;;
    esac

    # The server sets it on the console's database (SQLite or PostgreSQL);
    # the password goes in on standard input, never in the arguments
    local success=false
    if printf '%s\n' "$new_password" | server_cmd --as-service reset-password admin; then
        success=true
    fi

    echo ""
    if [ "$success" = "true" ]; then
        # DEFAULT_ADMIN_PASSWORD in .env must match, or the next start with a
        # forced password update would set the old one again
        if grep -q '^DEFAULT_ADMIN_PASSWORD=' "$CONSOLE_PATH/.env" 2>/dev/null; then
            # awk handles passwords with special characters (|, &, $, etc.)
            awk -v pw="$new_password" '/^DEFAULT_ADMIN_PASSWORD=/{print "DEFAULT_ADMIN_PASSWORD=" pw; next}{print}' "$CONSOLE_PATH/.env" > "$CONSOLE_PATH/.env.tmp" \
                && cat "$CONSOLE_PATH/.env.tmp" > "$CONSOLE_PATH/.env" && rm -f "$CONSOLE_PATH/.env.tmp"
        fi

        echo -e "${GREEN}╔════════════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║              NEW LOGIN CREDENTIALS                       ║${NC}"
        echo -e "${GREEN}╠════════════════════════════════════════════════════════╣${NC}"
        echo -e "${GREEN}║  Login:    ${WHITE}admin${GREEN}                                     ║${NC}"
        echo -e "${GREEN}║  Password: ${WHITE}${new_password}${GREEN}                         ║${NC}"
        echo -e "${GREEN}╚════════════════════════════════════════════════════════╝${NC}"

        # Persist credentials only when explicitly requested.
        if [ "$STORE_ADMIN_CREDENTIALS" = "true" ]; then
            cat > "$RUSTDESK_PATH/.admin_credentials" << CREDEOF
Admin Username: admin
Admin Password: $new_password
Generated by: BetterDesk password reset
Timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)
CREDEOF
            chmod 600 "$RUSTDESK_PATH/.admin_credentials"
        fi
    else
        print_error "Failed to reset password!"
        print_info "Check the database settings in $CONSOLE_PATH/.env"
    fi

    press_enter
}

#===============================================================================
# Build Functions
#===============================================================================

# Legacy Support Agent source staging — Generator now downloads BetterDesk-Client
# templates into data/modules/betterdesk-support-generator/. Kept as a no-op so
# older install/update call sites do not fail.
stage_support_agent_source() {
    print_info "Support Generator uses Client templates (module install); skipping Go support-agent staging"
    return 0
}

# Optional developer toolchain for local embedded BetterDesk-Client builds.
# Production installs should Import latest release in Generator instead —
# signed agents come from GitHub (BetterDesk-Client), not from this host.
do_install_build_toolchain() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ AGENT BUILD TOOLCHAIN (OPTIONAL / DEV) ══════════${NC}"
    echo ""
    echo "  Production: use Generator → Import latest release"
    echo "  (signed incoming-only agents from BetterDesk-Client)."
    echo "  This host does NOT need Flutter/Cargo for that path."
    echo ""
    echo "  This menu is for maintainers who set"
    echo "  BETTERDESK_CLIENT_GENERATOR_MODE=embedded and compile"
    echo "  Support agents locally (large download; not required)."
    echo ""
    echo "  Disk:  ~3 GB download, ~5 GB after install,"
    echo "         plus cargo build cache (/var/cache/betterdesk-build)."
    echo ""

    local script_dir
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    local toolchain_script="$script_dir/scripts/install-build-toolchain.sh"

    if [ ! -f "$toolchain_script" ]; then
        print_error "Toolchain installer not found: $toolchain_script"
        print_info "Pull the latest repository and try again."
        press_enter
        return 1
    fi

    if [ "$AUTO_MODE" != true ]; then
        read -p "Proceed with toolchain install? [y/N]: " confirm
        confirm="${confirm:-N}"
        if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
            print_info "Cancelled."
            press_enter
            return 0
        fi
    fi

    local extra_args=()
    [ "$AUTO_MODE" = true ] && extra_args+=(--unattended)

    BUILD_USER="${SUDO_USER:-${BUILD_USER:-unitronix}}" \
        bash "$toolchain_script" "${extra_args[@]}"

    local rc=$?
    if [ $rc -eq 0 ]; then
        stage_support_agent_source "$script_dir" || true
        print_success "Build toolchain installed."
        print_info "Restart the server to pick up the new PATH:"
        print_info "  sudo systemctl restart betterdesk-server"
    else
        print_error "Toolchain installer exited with code $rc"
    fi
    press_enter
    return $rc
}

do_build() {
    local _menu_items=(
        $'Rebuild & deploy server\tCompile, stop, replace and restart the Go server'
        $'Compile server only\tBuild the Go binary without deploying it'
        $'Build legacy Rust binaries\tArchived hbbs/hbbr (advanced)'
        $'Back\tReturn to the main menu'
    )
    local _menu_returns=( 1 2 3 0 )
    menu_choose "Build & Deploy" "Rebuild and deploy the BetterDesk Go server"
    local build_choice="${MENU_CHOICE:-1}"

    case $build_choice in
        1) do_rebuild_go_server ;;
        2) do_compile_go_only ;;
        3) do_build_legacy_rust ;;
        0) return ;;
        *) print_warning "Invalid option"; sleep 1 ;;
    esac
}

# Rebuild & deploy Go server: compile → backup → stop → replace → start → verify
do_rebuild_go_server() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ REBUILD & DEPLOY GO SERVER ══════════${NC}"
    echo ""

    detect_installation

    if [ "$INSTALL_STATUS" = "none" ]; then
        print_warning "BetterDesk is not installed. Binary will be compiled but not deployed."
        if ! confirm "Continue with compilation only?"; then
            press_enter
            return
        fi
        do_compile_go_only
        return
    fi

    # Step 1: Compile
    print_step "[1/5] Compiling Go server from source..."
    detect_architecture

    if ! compile_go_server; then
        print_error "Compilation failed — aborting. Current installation is untouched."
        press_enter
        return
    fi

    local new_binary="$GO_SERVER_SOURCE/betterdesk-server"
    if [ ! -f "$new_binary" ]; then
        print_error "Compiled binary not found at $new_binary"
        press_enter
        return
    fi

    # Step 2: Backup current binary
    print_step "[2/5] Backing up current binary..."
    local installed_binary="$RUSTDESK_PATH/betterdesk-server"
    local ts
    ts=$(date +%Y%m%d_%H%M%S)
    if [ -f "$installed_binary" ]; then
        cp "$installed_binary" "${installed_binary}.backup.${ts}"
        print_info "Backup: ${installed_binary}.backup.${ts}"
    else
        print_info "No existing binary to backup"
    fi

    # Step 3: Stop services
    print_step "[3/5] Stopping services..."
    graceful_stop_services

    # Step 4: Replace binary
    print_step "[4/5] Deploying new binary..."
    mkdir -p "$RUSTDESK_PATH"
    cp "$new_binary" "$installed_binary"
    chmod +x "$installed_binary"
    local size
    size=$(du -h "$installed_binary" | cut -f1)
    print_success "Deployed: $installed_binary ($size)"

    # Step 5: Start services and verify
    print_step "[5/5] Starting services..."
    start_services_with_verification

    if systemctl is-active --quiet betterdesk-server 2>/dev/null; then
        echo ""
        print_success "Go server rebuilt and deployed successfully!"
        echo ""
        echo -e "${WHITE}Recent logs:${NC}"
        journalctl -u betterdesk-server -n 5 --no-pager 2>/dev/null || true
    else
        print_error "Service failed to start after rebuild!"
        echo ""
        echo -e "${YELLOW}Rolling back to previous binary...${NC}"
        if [ -f "${installed_binary}.backup.${ts}" ]; then
            cp "${installed_binary}.backup.${ts}" "$installed_binary"
            chmod +x "$installed_binary"
            systemctl start betterdesk-server 2>/dev/null || true
            sleep 2
            if systemctl is-active --quiet betterdesk-server 2>/dev/null; then
                print_success "Rollback successful — previous binary restored"
            else
                print_error "Rollback also failed. Check: journalctl -u betterdesk-server -n 50"
            fi
        else
            print_error "No backup to rollback to. Check: journalctl -u betterdesk-server -n 50"
        fi
    fi

    press_enter
}

# Compile Go server only (no deployment)
do_compile_go_only() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ COMPILE GO SERVER ══════════${NC}"
    echo ""

    detect_architecture

    if ! compile_go_server; then
        print_error "Compilation failed"
        press_enter
        return
    fi

    local new_binary="$GO_SERVER_SOURCE/betterdesk-server"
    local size
    size=$(du -h "$new_binary" | cut -f1)
    print_success "Binary compiled: $new_binary ($size)"
    print_info "Use option 7 → 1 to deploy it, or copy manually."

    press_enter
}

# Legacy Rust build (archived — hbbs/hbbr)
do_build_legacy_rust() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ BUILD LEGACY RUST BINARIES ══════════${NC}"
    echo ""
    print_warning "Legacy Rust binaries (hbbs/hbbr) are archived."
    print_info "The Go server is the current architecture."
    echo ""
    if ! confirm "Continue with legacy Rust build anyway?"; then
        return
    fi

    # Check Rust
    if ! command -v cargo &> /dev/null; then
        print_warning "Rust is not installed!"
        echo ""
        if confirm "Do you want to install Rust?"; then
            curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
            source "$HOME/.cargo/env"
        else
            press_enter
            return
        fi
    fi

    print_info "Rust: $(cargo --version)"
    echo ""

    local build_dir="/tmp/betterdesk_build_$$"
    mkdir -p "$build_dir"
    cd "$build_dir"

    print_step "Downloading RustDesk Server sources..."
    git clone --depth 1 --branch 1.1.14 https://github.com/rustdesk/rustdesk-server.git
    cd rustdesk-server
    git submodule update --init --recursive

    print_step "Applying BetterDesk modifications..."

    # Copy modified sources
    if [ -d "$SCRIPT_DIR/hbbs-patch-v2/src" ]; then
        cp "$SCRIPT_DIR/hbbs-patch-v2/src/main.rs" src/ 2>/dev/null || true
        cp "$SCRIPT_DIR/hbbs-patch-v2/src/http_api.rs" src/ 2>/dev/null || true
        cp "$SCRIPT_DIR/hbbs-patch-v2/src/database.rs" src/ 2>/dev/null || true
        cp "$SCRIPT_DIR/hbbs-patch-v2/src/peer.rs" src/ 2>/dev/null || true
        cp "$SCRIPT_DIR/hbbs-patch-v2/src/rendezvous_server.rs" src/ 2>/dev/null || true
    else
        print_error "Modified sources not found in hbbs-patch-v2/src/"
        press_enter
        return
    fi

    print_step "Compiling (may take several minutes)..."
    cargo build --release

    # Copy results
    print_step "Copying binaries..."
    detect_architecture
    mkdir -p "$SCRIPT_DIR/hbbs-patch-v2"

    cp target/release/hbbs "$SCRIPT_DIR/hbbs-patch-v2/hbbs-linux-$ARCH_NAME"
    cp target/release/hbbr "$SCRIPT_DIR/hbbs-patch-v2/hbbr-linux-$ARCH_NAME"

    # Cleanup
    cd /
    rm -rf "$build_dir"

    print_success "Legacy Rust compilation completed!"
    print_info "Binaries saved in: $SCRIPT_DIR/hbbs-patch-v2/"

    press_enter
}

#===============================================================================
# Firewall Configuration
#===============================================================================

configure_firewall_rules() {
    local required_ports="${GO_API_PORT:-21114} ${CLIENT_API_PORT:-21121} 21115 21116 21117 21118 21119 5000 5443"
    local created=0
    local total=0
    
    if command -v ufw &>/dev/null && ufw status 2>/dev/null | grep -q "active"; then
        print_info "Configuring UFW firewall rules..."
        
        for port in $required_ports; do
            total=$((total + 1))
            if ! ufw status 2>/dev/null | grep -qE "^${port}[/ ]"; then
                if [ "$port" = "21116" ]; then
                    ufw allow 21116/tcp comment "BetterDesk ID Server TCP" 2>/dev/null && created=$((created + 1))
                    ufw allow 21116/udp comment "BetterDesk ID Server UDP" 2>/dev/null && created=$((created + 1))
                    total=$((total + 1))
                else
                    ufw allow "${port}/tcp" comment "BetterDesk port ${port}" 2>/dev/null && created=$((created + 1))
                fi
            fi
        done
        
        ufw reload 2>/dev/null
        
    elif command -v firewall-cmd &>/dev/null && systemctl is-active --quiet firewalld 2>/dev/null; then
        print_info "Configuring firewalld rules..."
        
        for port in $required_ports; do
            total=$((total + 1))
            local open_ports=$(firewall-cmd --list-ports 2>/dev/null)
            if ! echo "$open_ports" | grep -qE "${port}/tcp"; then
                if [ "$port" = "21116" ]; then
                    firewall-cmd --permanent --add-port=21116/tcp 2>/dev/null && created=$((created + 1))
                    firewall-cmd --permanent --add-port=21116/udp 2>/dev/null && created=$((created + 1))
                    total=$((total + 1))
                else
                    firewall-cmd --permanent --add-port="${port}/tcp" 2>/dev/null && created=$((created + 1))
                fi
            fi
        done
        
        firewall-cmd --reload 2>/dev/null
        
    elif command -v iptables &>/dev/null; then
        print_info "Configuring iptables rules..."
        
        for port in $required_ports; do
            total=$((total + 1))
            if ! iptables -L INPUT -n 2>/dev/null | grep -qE "dpt:${port}\b"; then
                if [ "$port" = "21116" ]; then
                    iptables -A INPUT -p tcp --dport 21116 -j ACCEPT 2>/dev/null && created=$((created + 1))
                    iptables -A INPUT -p udp --dport 21116 -j ACCEPT 2>/dev/null && created=$((created + 1))
                    total=$((total + 1))
                else
                    iptables -A INPUT -p tcp --dport "$port" -j ACCEPT 2>/dev/null && created=$((created + 1))
                fi
            fi
        done
        
        # Try to persist iptables rules
        if command -v iptables-save &>/dev/null; then
            iptables-save > /etc/iptables/rules.v4 2>/dev/null || \
            iptables-save > /etc/sysconfig/iptables 2>/dev/null || true
        fi
    else
        print_info "No active firewall detected — no rules to configure"
        return 0
    fi
    
    if [ $created -gt 0 ]; then
        print_success "Created $created firewall rule(s)"
    else
        print_success "All firewall rules already configured"
    fi
    
    return 0
}

#===============================================================================
# Diagnostics Functions
#===============================================================================

do_diagnostics() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ DIAGNOSTICS ══════════${NC}"
    echo ""
    
    print_status
    
    echo ""
    echo -e "${WHITE}${BOLD}═══ Service logs (last 10 lines) ═══${NC}"
    echo ""
    
    # Check for Go server first, then legacy Rust services
    if systemctl list-unit-files betterdesk-server.service &>/dev/null 2>&1; then
        echo -e "${CYAN}--- betterdesk-server (Go) ---${NC}"
        journalctl -u betterdesk-server -n 10 --no-pager 2>/dev/null || echo "No logs found"
    else
        echo -e "${CYAN}--- rustdesksignal (Legacy Rust) ---${NC}"
        journalctl -u rustdesksignal -n 10 --no-pager 2>/dev/null || echo "No logs found"
        
        echo ""
        echo -e "${CYAN}--- rustdeskrelay (Legacy Rust) ---${NC}"
        journalctl -u rustdeskrelay -n 10 --no-pager 2>/dev/null || echo "No logs found"
    fi
    
    echo ""
    
    echo ""
    echo -e "${WHITE}${BOLD}═══ Database statistics ═══${NC}"
    echo ""
    
    # Determine the active database type the SAME way the rest of the script does:
    # read DB_TYPE from the console .env first (source of truth), and only fall back
    # to SQLite file detection. This prevents a stale db_v2.sqlite3 left over from a
    # previous install from masking an active PostgreSQL backend.
    local diag_db_type="sqlite"
    local diag_pg_uri=""
    if [ -f "$CONSOLE_PATH/.env" ]; then
        diag_db_type=$(grep -m1 '^DB_TYPE=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')
        diag_db_type="${diag_db_type:-sqlite}"
        diag_pg_uri=$(grep -m1 '^DATABASE_URL=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2-)
    fi
    # The Go server may also carry the DSN in its systemd unit (-db postgres://...)
    if [ "$diag_db_type" != "postgres" ] && [ -f /etc/systemd/system/betterdesk-server.service ]; then
        local svc_db
        svc_db=$(grep -oP '\-db\s+"?\K(postgres|postgresql)://[^" ]+' /etc/systemd/system/betterdesk-server.service 2>/dev/null | head -1)
        if [ -n "$svc_db" ]; then
            diag_db_type="postgres"
            diag_pg_uri="${diag_pg_uri:-$svc_db}"
        fi
    fi
    
    if [ "$diag_db_type" = "postgres" ] && [ -n "$diag_pg_uri" ]; then
        # Mask password for display
        local diag_pg_display
        diag_pg_display=$(echo "$diag_pg_uri" | sed 's|://[^:]*:[^@]*@|://***:***@|')
        echo -e "  Database type:     ${CYAN}PostgreSQL${NC}"
        echo -e "  Connection:        ${DIM}$diag_pg_display${NC}"
        if command -v psql &>/dev/null; then
            if PGCONNECT_TIMEOUT=3 psql "$diag_pg_uri" -tAc "SELECT 1" &>/dev/null; then
                local device_count online_count user_count
                device_count=$(PGCONNECT_TIMEOUT=3 psql "$diag_pg_uri" -tAc "SELECT COUNT(*) FROM peers WHERE soft_deleted = FALSE" 2>/dev/null || echo "0")
                online_count=$(PGCONNECT_TIMEOUT=3 psql "$diag_pg_uri" -tAc "SELECT COUNT(*) FROM peers WHERE soft_deleted = FALSE AND status = 'ONLINE'" 2>/dev/null || echo "0")
                user_count=$(PGCONNECT_TIMEOUT=3 psql "$diag_pg_uri" -tAc "SELECT COUNT(*) FROM users" 2>/dev/null || echo "0")
                echo -e "  Status:            ${GREEN}Connected${NC}"
                echo "  Devices:           ${device_count:-0}"
                echo "  Online:            ${online_count:-0}"
                echo "  Users:             ${user_count:-0}"
            else
                echo -e "  Status:            ${RED}Connection failed${NC}"
                echo -e "  ${YELLOW}Tip: verify the PostgreSQL service and DATABASE_URL credentials${NC}"
            fi
        else
            echo -e "  ${YELLOW}Install the 'psql' client to see live database statistics${NC}"
        fi
    elif [ -f "$DB_PATH" ]; then
        local device_count online_count user_count
        device_count=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM peers WHERE soft_deleted = 0" 2>/dev/null || \
                            sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM peers WHERE is_deleted = 0" 2>/dev/null || \
                            sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM peer WHERE is_deleted = 0" 2>/dev/null || echo "0")
        online_count=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM peers WHERE soft_deleted = 0 AND status = 'ONLINE'" 2>/dev/null || \
                            sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM peers WHERE status = 1 AND is_deleted = 0" 2>/dev/null || \
                            sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM peer WHERE status = 1 AND is_deleted = 0" 2>/dev/null || echo "0")
        user_count=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM users" 2>/dev/null || echo "0")
        
        echo -e "  Database type:     ${CYAN}SQLite${NC}"
        echo -e "  File:              ${DIM}$DB_PATH${NC}"
        echo "  Devices:           $device_count"
        echo "  Online:            $online_count"
        echo "  Users:             $user_count"
    else
        echo -e "  Database type:     ${CYAN}SQLite${NC} (configured)"
        echo -e "  ${YELLOW}SQLite database file not found: $DB_PATH${NC}"
        echo -e "  ${DIM}This is normal before the first device registers.${NC}"
    fi
    
    # --- Port diagnostics ---
    echo ""
    echo -e "${WHITE}${BOLD}═══ Port diagnostics ═══${NC}"
    echo ""
    
    local port_issues=0
    local port_defs=(
        "${GO_API_PORT:-21114}:TCP:betterdesk-serv|betterdesk-server|hbbs:Go HTTP API (handlers)"
        "${CLIENT_API_PORT:-21121}:TCP:node|MainThread:Client API compat proxy → Go"
        "21115:TCP:betterdesk-serv|betterdesk-server|hbbs:NAT Test"
        "21116:TCP:betterdesk-serv|betterdesk-server|hbbs:ID Server (TCP)"
        "21116:UDP:betterdesk-serv|betterdesk-server|hbbs:ID Server (UDP)"
        "21117:TCP:betterdesk-serv|betterdesk-server|hbbr:Relay Server"
        "5000:TCP:node|MainThread:Web Console"
    )
    
    for entry in "${port_defs[@]}"; do
        IFS=':' read -r port proto expected desc <<< "$entry"
        
        local listening=false
        local proc_info=""
        
        if [ "$proto" = "TCP" ]; then
            proc_info=$(ss -tlnp 2>/dev/null | grep ":${port} " | head -1)
            [ -z "$proc_info" ] && proc_info=$(netstat -tlnp 2>/dev/null | grep ":${port} " | head -1)
        else
            proc_info=$(ss -ulnp 2>/dev/null | grep ":${port} " | head -1)
            [ -z "$proc_info" ] && proc_info=$(netstat -ulnp 2>/dev/null | grep ":${port} " | head -1)
        fi
        
        if [ -n "$proc_info" ]; then
            listening=true
            local process_name=$(echo "$proc_info" | grep -oP 'users:\(\("\K[^"]+' 2>/dev/null || \
                                echo "$proc_info" | awk '{print $NF}')
        fi
        
        printf "  Port %s/%s (%-18s): " "$port" "$proto" "$desc"
        
        if $listening; then
            if [ -n "$process_name" ] && echo "$process_name" | grep -qiE "$expected"; then
                echo -e "${GREEN}OK - $process_name${NC}"
            elif [ -n "$process_name" ]; then
                echo -e "${RED}CONFLICT - used by $process_name${NC}"
                port_issues=$((port_issues + 1))
            else
                echo -e "${GREEN}LISTENING${NC}"
            fi
        else
            echo -e "${YELLOW}NOT LISTENING${NC}"
        fi
    done
    
    if [ $port_issues -gt 0 ]; then
        echo ""
        print_warning "$port_issues port conflict(s) detected!"
        echo -e "  ${YELLOW}Tip: Stop conflicting processes or change ports in configuration${NC}"
    fi
    
    # --- Firewall diagnostics ---
    echo ""
    echo -e "${WHITE}${BOLD}═══ Firewall status ═══${NC}"
    echo ""
    
    local fw_type="none"
    local missing_rules=0
    local required_ports="${GO_API_PORT:-21114} ${CLIENT_API_PORT:-21121} 21115 21116 21117 5000"
    
    if command -v ufw &>/dev/null && ufw status 2>/dev/null | grep -q "active"; then
        fw_type="ufw"
        echo -e "  Firewall: ${YELLOW}UFW (active)${NC}"
        echo ""
        
        for port in $required_ports; do
            local status_line=$(ufw status 2>/dev/null | grep -E "^${port}[/ ]")
            printf "  Port %-5s: " "$port"
            if [ -n "$status_line" ]; then
                echo -e "${GREEN}ALLOWED${NC}"
            else
                echo -e "${RED}NO RULE${NC}"
                missing_rules=$((missing_rules + 1))
            fi
        done
        
    elif command -v firewall-cmd &>/dev/null && systemctl is-active --quiet firewalld 2>/dev/null; then
        fw_type="firewalld"
        echo -e "  Firewall: ${YELLOW}firewalld (active)${NC}"
        echo ""
        
        local open_ports=$(firewall-cmd --list-ports 2>/dev/null)
        for port in $required_ports; do
            printf "  Port %-5s: " "$port"
            if echo "$open_ports" | grep -qE "${port}/tcp|${port}/udp"; then
                echo -e "${GREEN}ALLOWED${NC}"
            else
                echo -e "${RED}NO RULE${NC}"
                missing_rules=$((missing_rules + 1))
            fi
        done
        
    elif iptables -L INPUT -n 2>/dev/null | grep -q "ACCEPT"; then
        fw_type="iptables"
        echo -e "  Firewall: ${YELLOW}iptables${NC}"
        echo ""
        
        for port in $required_ports; do
            printf "  Port %-5s: " "$port"
            if iptables -L INPUT -n 2>/dev/null | grep -qE "dpt:${port}\b"; then
                echo -e "${GREEN}ALLOWED${NC}"
            else
                echo -e "${RED}NO RULE / CHECK MANUALLY${NC}"
                missing_rules=$((missing_rules + 1))
            fi
        done
    else
        echo -e "  Firewall: ${GREEN}No active firewall detected (all ports open)${NC}"
    fi
    
    if [ $missing_rules -gt 0 ]; then
        echo ""
        print_warning "$missing_rules firewall rule(s) missing!"
        echo -e "  ${YELLOW}Use option 'F' below to auto-configure firewall${NC}"
    fi
    
    # --- API connectivity test ---
    echo ""
    echo -e "${WHITE}${BOLD}═══ API connectivity ═══${NC}"
    echo ""
    
    local api_port="${GO_API_PORT:-21114}"
    
    # Detect if Go server API uses TLS (only if explicit --tls-api in service args)
    local api_use_tls=false
    local api_scheme="http"
    if systemctl cat betterdesk-server.service 2>/dev/null | grep -qE '\-tls-api'; then
        api_use_tls=true
        api_scheme="https"
    fi
    
    printf "  Go Server API (%s %s):  " "$api_scheme" "$api_port"
    if [ "$api_use_tls" = true ]; then
        if curl -skfo /dev/null --connect-timeout 3 "https://127.0.0.1:${api_port}/api/health" 2>/dev/null; then
            echo -e "${GREEN}OK (HTTPS)${NC}"
        else
            # Fallback: try HTTP in case TLS is only on signal/relay
            if curl -sfo /dev/null --connect-timeout 3 "http://127.0.0.1:${api_port}/api/health" 2>/dev/null; then
                echo -e "${GREEN}OK (HTTP)${NC}"
                echo -e "  ${YELLOW}⚠ Note: Go server has TLS cert but API responds on HTTP${NC}"
            else
                echo -e "${RED}UNREACHABLE${NC}"
                echo -e "  ${YELLOW}Tip: Check betterdesk-server logs: journalctl -u betterdesk-server -n 20${NC}"
            fi
        fi
    else
        if curl -sfo /dev/null --connect-timeout 3 "http://127.0.0.1:${api_port}/api/health" 2>/dev/null; then
            echo -e "${GREEN}OK${NC}"
        else
            echo -e "${RED}UNREACHABLE${NC}"
        fi
    fi
    
    printf "  Web Console (5000):    "
    if curl -sfo /dev/null --connect-timeout 3 "http://127.0.0.1:5000/health" 2>/dev/null; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${RED}UNREACHABLE${NC}"
    fi
    
    # --- TLS mismatch detection ---
    if [ "$api_use_tls" = true ]; then
        local console_api_url=""
        # Check what URL the console is configured to use
        if [ -f /etc/systemd/system/betterdesk-console.service ]; then
            console_api_url=$(grep 'BETTERDESK_API_URL=' /etc/systemd/system/betterdesk-console.service 2>/dev/null | tail -1 | sed 's/.*BETTERDESK_API_URL=//')
        fi
        if [ -z "$console_api_url" ] && [ -f "$CONSOLE_PATH/.env" ]; then
            console_api_url=$(grep -m1 '^BETTERDESK_API_URL=' "$CONSOLE_PATH/.env" 2>/dev/null | cut -d= -f2-)
        fi
        
        if [ -n "$console_api_url" ] && echo "$console_api_url" | grep -q '^http://'; then
            echo ""
            print_warning "TLS MISMATCH DETECTED!"
            echo -e "  ${YELLOW}Go server has TLS enabled but console is configured with HTTP:${NC}"
            echo -e "  ${YELLOW}  Console URL: $console_api_url${NC}"
            echo -e "  ${YELLOW}  Expected:    https://localhost:$api_port/api${NC}"
            echo -e "  ${YELLOW}  Fix: Re-run installation (option 1) or update .env and systemd service${NC}"
        fi
    fi
    
    # --- Diagnostics sub-menu ---
    echo ""
    local _menu_items=(
        $'Configure firewall rules\tAuto-create any missing firewall rules'
        $'Test external ports\tCheck port connectivity from outside'
        $'Back\tReturn to the main menu'
    )
    local _menu_returns=( F P 0 )
    menu_choose "Diagnostics Actions" "Optional follow-up checks"
    local sub_choice="$MENU_CHOICE"
    
    case "$sub_choice" in
        [Ff])
            echo ""
            configure_firewall_rules
            press_enter
            ;;
        [Pp])
            echo ""
            echo -e "${WHITE}${BOLD}═══ External port test ═══${NC}"
            echo ""
            local server_ip=$(get_public_ip)
            print_info "Public IP: $server_ip"
            print_info "Testing external port accessibility..."
            echo ""
            
            for port in 21115 21116 21117; do
                printf "  Port %s: " "$port"
                if timeout 3 bash -c "echo >/dev/tcp/$server_ip/$port" 2>/dev/null; then
                    echo -e "${GREEN}REACHABLE${NC}"
                else
                    echo -e "${RED}BLOCKED/UNREACHABLE${NC}"
                fi
            done
            press_enter
            ;;
        *)
            return
            ;;
    esac
}

#===============================================================================
# Uninstall Functions
#===============================================================================

do_uninstall() {
    print_header
    echo -e "${RED}${BOLD}══════════ UNINSTALL ══════════${NC}"
    echo ""
    
    print_warning "This operation will remove BetterDesk Console!"
    echo ""
    
    if [ "$AUTO_MODE" != true ] && [ "$UNINSTALL_MODE" != true ]; then
        if ! confirm "Are you sure you want to continue?"; then
            return
        fi
    fi
    
    if [ "$AUTO_MODE" = true ] || confirm "Create backup before uninstall?"; then
        do_backup_silent
    fi
    
    print_step "Stopping services..."
    # Stop Go server (primary)
    systemctl stop betterdesk-server betterdesk-console 2>/dev/null || true
    systemctl disable betterdesk-server betterdesk-console 2>/dev/null || true
    # Stop legacy Rust services if they exist
    systemctl stop rustdesksignal rustdeskrelay betterdesk betterdesk-api betterdesk-go 2>/dev/null || true
    systemctl disable rustdesksignal rustdeskrelay betterdesk betterdesk-api betterdesk-go 2>/dev/null || true
    
    print_step "Removing service files..."
    # Remove Go services
    rm -f /etc/systemd/system/betterdesk-server.service
    rm -f /etc/systemd/system/betterdesk-console.service
    # Remove legacy services
    rm -f /etc/systemd/system/rustdesksignal.service
    rm -f /etc/systemd/system/rustdeskrelay.service
    rm -f /etc/systemd/system/betterdesk.service
    rm -f /etc/systemd/system/betterdesk-api.service
    rm -f /etc/systemd/system/betterdesk-go.service
    systemctl daemon-reload
    
    if [ "$PURGE_MODE" = true ] || { [ "$AUTO_MODE" != true ] && confirm "Remove installation files ($RUSTDESK_PATH)?"; }; then
        rm -rf "$RUSTDESK_PATH"
        print_info "Removed: $RUSTDESK_PATH"
    else
        print_info "Preserved server data: $RUSTDESK_PATH"
    fi
    
    if [ "$PURGE_MODE" = true ] || { [ "$AUTO_MODE" != true ] && confirm "Remove Web Console ($CONSOLE_PATH)?"; }; then
        rm -rf "$CONSOLE_PATH"
        print_info "Removed: $CONSOLE_PATH"
    else
        print_info "Preserved console data: $CONSOLE_PATH"
    fi
    
    print_success "BetterDesk has been uninstalled"
    press_enter
}

#===============================================================================
# SSL Certificate Configuration
#===============================================================================

do_configure_ssl() {
    maybe_reexec_if_installer_on_disk_is_newer
    print_header
    echo -e "${WHITE}${BOLD}══════════ SSL CERTIFICATE CONFIGURATION ══════════${NC}"
    echo ""

    if [ ! -f "$CONSOLE_PATH/.env" ]; then
        print_error "Console .env not found at $CONSOLE_PATH/.env"
        print_info "Please install BetterDesk first (option 1)"
        press_enter
        return
    fi

    local ssl_dir="$RUSTDESK_PATH/ssl"
    local env_file="$CONSOLE_PATH/.env"
    local svc_file="/etc/systemd/system/betterdesk-console.service"
    local ssl_tls_active="no"

    local _menu_items=(
        $'Let\'s Encrypt\tAutomatic cert (needs domain name + port 80)'
        $'Custom certificate\tProvide your own cert + key files'
        $'Self-signed certificate\tLAN / testing only'
        $'Disable SSL\tRevert the console to plain HTTP'
        $'Enterprise TLS\tPanel + signal + relay TLS (API stays HTTP)'
        $'External reverse proxy\tTLS at Caddy/Nginx — panel HTTP on localhost'
    )
    local _menu_returns=( 1 2 3 4 5 6 )
    menu_choose "SSL Certificate Configuration" "HTTPS for the panel, or TLS at an external reverse proxy"
    local ssl_choice="$MENU_CHOICE"

    case "${ssl_choice:-1}" in
        1)
            echo ""
            read -p "Enter your domain name (e.g., betterdesk.example.com): " domain
            if [ -z "$domain" ]; then
                print_error "Domain name required for Let's Encrypt"
                press_enter
                return
            fi

            if ! command -v certbot &> /dev/null; then
                print_step "Installing certbot..."
                if command -v apt-get &> /dev/null; then apt-get install -y certbot
                elif command -v dnf &> /dev/null; then dnf install -y certbot
                elif command -v yum &> /dev/null; then yum install -y certbot
                elif command -v pacman &> /dev/null; then pacman -Sy --noconfirm certbot
                else
                    print_error "Could not install certbot. Please install it manually."
                    press_enter
                    return
                fi
            fi

            print_step "Requesting certificate for $domain..."
            print_info "Port 80 must be accessible from the internet"

            certbot certonly --standalone --preferred-challenges http \
                -d "$domain" --non-interactive --agree-tos \
                --email "admin@$domain" 2>&1 || {
                    print_error "Certificate request failed. Make sure port 80 is open and the domain points to this server."
                    press_enter
                    return
                }

            local le_live_dir="/etc/letsencrypt/live/$domain"
            if ! deploy_ssl_material_to_rustdesk_dir \
                "$le_live_dir/fullchain.pem" "$le_live_dir/privkey.pem" "$le_live_dir"; then
                print_error "Failed to deploy Let's Encrypt certificate for console user"
                press_enter
                return
            fi

            apply_console_protocol_mode https "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key" true false
            ssl_tls_active="yes"

            if ! crontab -l 2>/dev/null | grep -q "certbot renew"; then
                (crontab -l 2>/dev/null; echo "0 3 * * * certbot renew --quiet") | crontab -
                print_info "Auto-renewal cron job added (daily at 3:00 AM)"
            fi

            print_success "Let's Encrypt certificate configured for $domain"
            print_info "Open the panel at https://${domain}:$(resolve_panel_https_port) (IP access will show certificate errors)"
            ;;
        2)
            echo ""
            read -p "Path to certificate file (PEM): " cert_path
            read -p "Path to private key file (PEM): " key_path
            read -p "Path to CA bundle (optional, press Enter to skip): " ca_path

            if [ ! -f "$cert_path" ] || [ ! -f "$key_path" ]; then
                print_error "Certificate or key file not found."
                press_enter
                return
            fi
            if ! openssl x509 -in "$cert_path" -noout 2>/dev/null; then
                print_error "The provided certificate is not a valid X.509 file."
                press_enter
                return
            fi

            local deploy_crt="$cert_path" merged_crt=""
            if [ -n "$ca_path" ] && [ -f "$ca_path" ]; then
                merged_crt=$(mktemp)
                cat "$cert_path" "$ca_path" > "$merged_crt"
                deploy_crt="$merged_crt"
            fi
            if ! deploy_ssl_material_to_rustdesk_dir "$deploy_crt" "$key_path"; then
                [ -n "$merged_crt" ] && rm -f "$merged_crt"
                print_error "Failed to deploy custom certificate for console user"
                press_enter
                return
            fi
            [ -n "$merged_crt" ] && rm -f "$merged_crt"

            infer_tls_mode_from_cert "$ssl_dir/betterdesk.crt"
            apply_console_protocol_mode https "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key" \
                "$INFERRED_RUSTDESK_API_TLS" "$INFERRED_ALLOW_SELF_SIGNED"
            if [ -n "$ca_path" ] && [ -f "$ca_path" ]; then
                _upsert_env_line "$env_file" SSL_CA_PATH "$ca_path"
            fi
            ssl_tls_active="yes"
            print_success "Custom SSL certificate configured"
            ;;
        3)
            mkdir -p "$ssl_dir"
            echo ""
            read -p "Enter domain name (optional, press Enter to skip): " cert_domain

            local server_ip lan_ip san_list cn
            server_ip=$(get_public_ip)
            lan_ip=$(ip -4 addr show scope global | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -1 2>/dev/null || \
                     hostname -I 2>/dev/null | awk '{print $1}' || echo "")
            san_list="IP:$server_ip,IP:127.0.0.1,DNS:localhost"
            [ -n "$lan_ip" ] && [ "$lan_ip" != "$server_ip" ] && san_list="$san_list,IP:$lan_ip"
            [ -n "$cert_domain" ] && san_list="DNS:$cert_domain,$san_list"
            cn="${cert_domain:-$server_ip}"

            print_step "Generating self-signed certificate..."
            print_info "SANs: $san_list"
            openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
                -keyout "$ssl_dir/betterdesk.key" \
                -out "$ssl_dir/betterdesk.crt" \
                -subj "/CN=$cn/O=BetterDesk/C=PL" \
                -addext "subjectAltName=$san_list" 2>/dev/null || \
            openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
                -keyout "$ssl_dir/betterdesk.key" \
                -out "$ssl_dir/betterdesk.crt" \
                -subj "/CN=$cn/O=BetterDesk/C=PL" 2>/dev/null

            if ! deploy_ssl_material_to_rustdesk_dir "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key"; then
                print_error "Failed to set permissions on self-signed certificate"
                press_enter
                return
            fi

            apply_console_protocol_mode https "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key" false true
            ssl_tls_active="yes"
            print_success "Self-signed certificate generated (valid 10 years)"
            print_info "Certificate: $ssl_dir/betterdesk.crt"
            [ -n "$lan_ip" ] && [ "$lan_ip" != "$server_ip" ] && print_info "LAN IP included: $lan_ip"
            print_warning "Browsers will show security warning. Use Let's Encrypt for public servers."
            ;;
        4)
            apply_console_protocol_mode http
            clear_go_server_signal_relay_tls
            print_success "SSL disabled. Running in HTTP mode."
            print_info "If your browser still redirects to HTTPS, clear site cache or HSTS (chrome://net-internals/#hsts)"
            ;;
        5)
            print_header "Enterprise TLS Configuration"
            echo ""
            print_warning "IMPORTANT: Go API port ${GO_API_PORT:-21114} stays HTTP for RustDesk client compatibility."
            echo ""

            mkdir -p "$ssl_dir"
            read -p "Enter domain name (optional, press Enter to skip): " cert_domain

            local server_ip lan_ip san_list cn
            server_ip=$(get_public_ip)
            lan_ip=$(ip -4 addr show scope global | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -1 2>/dev/null || \
                     hostname -I 2>/dev/null | awk '{print $1}' || echo "")
            san_list="IP:$server_ip,IP:127.0.0.1,DNS:localhost"
            [ -n "$lan_ip" ] && [ "$lan_ip" != "$server_ip" ] && san_list="$san_list,IP:$lan_ip"
            [ -n "$cert_domain" ] && san_list="DNS:$cert_domain,$san_list"
            cn="${cert_domain:-$server_ip}"

            print_step "Generating Enterprise certificate..."
            print_info "SANs: $san_list"
            openssl req -x509 -nodes -days 3650 -newkey rsa:4096 \
                -keyout "$ssl_dir/betterdesk.key" \
                -out "$ssl_dir/betterdesk.crt" \
                -subj "/CN=$cn/O=BetterDesk Enterprise/C=PL" \
                -addext "subjectAltName=$san_list" 2>/dev/null || \
            openssl req -x509 -nodes -days 3650 -newkey rsa:4096 \
                -keyout "$ssl_dir/betterdesk.key" \
                -out "$ssl_dir/betterdesk.crt" \
                -subj "/CN=$cn/O=BetterDesk Enterprise/C=PL" 2>/dev/null

            if ! deploy_ssl_material_to_rustdesk_dir "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key"; then
                print_error "Failed to set permissions on Enterprise certificate"
                press_enter
                return
            fi

            apply_console_protocol_mode https "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key" true true
            _upsert_env_line "$env_file" ENTERPRISE_TLS true
            if [ -f "$svc_file" ]; then
                _upsert_systemd_env "$svc_file" ENTERPRISE_TLS true
            fi
            ssl_tls_active="yes"

            print_success "Enterprise TLS configured successfully!"
            echo ""
            print_info "Certificate: $ssl_dir/betterdesk.crt"
            print_info "Private key: $ssl_dir/betterdesk.key"
            [ -n "$lan_ip" ] && [ "$lan_ip" != "$server_ip" ] && print_info "LAN IP: $lan_ip"
            echo ""
            print_info "  Panel HTTPS: :$(resolve_panel_https_port)"
            print_info "  Signal TLS:  :21116"
            print_info "  Relay TLS:   :21117"
            print_info "  Go API HTTP: :${GO_API_PORT:-21114} (RustDesk client compatibility)"
            ;;
        6)
            do_configure_reverse_proxy || true
            ;;
        *)
            print_warning "Invalid option"
            press_enter
            return
            ;;
    esac

    if [ "$ssl_tls_active" = "yes" ]; then
        sync_go_server_signal_relay_tls "$ssl_dir"
        ensure_service_user >/dev/null
        print_info "Signal/relay TLS enabled; Go API stays HTTP (RustDesk client compatibility)"
        maybe_offer_standard_https_port
    fi

    echo ""
    if confirm "Restart BetterDesk to apply changes?"; then
        repair_https_stuck_state yes
        ensure_console_tls_material_readable 2>/dev/null || true
        systemctl restart betterdesk-server 2>/dev/null || true
        print_success "BetterDesk services restarted"
        verify_service_health "betterdesk-server" "21116" 15 >/dev/null 2>&1 || true
        verify_go_install 60 >/dev/null 2>&1 || true
        run_protocol_tests
    fi

    press_enter
}

#===============================================================================
# Protocol verification test-suite
#===============================================================================
# Runs a series of non-destructive connectivity / certificate checks after an
# HTTP <-> HTTPS switch so the operator gets immediate, trustworthy feedback.
# Honours the project invariant: the Go API (:21121) must remain HTTP for RustDesk clients.
run_protocol_tests() {
    local go_svc_file="$SERVER_UNIT"
    local ssl_dir="$RUSTDESK_PATH/ssl"
    local pass=0 fail=0 warn=0

    echo ""
    echo -e "${WHITE}${BOLD}═══ Post-configuration tests ═══${NC}"
    echo ""

    _test_ok()   { echo -e "  ${GREEN}✓${NC} $1"; pass=$((pass+1)); }
    _test_fail() { echo -e "  ${RED}✗${NC} $1"; fail=$((fail+1)); }
    _test_warn() { echo -e "  ${YELLOW}!${NC} $1"; warn=$((warn+1)); }

    # ── 1. Services running ──
    if systemctl is-active --quiet betterdesk-server 2>/dev/null; then
        _test_ok "Go server service is active"
    else
        _test_fail "Go server service is NOT active (journalctl -u betterdesk-server)"
        if journalctl -u betterdesk-server --no-pager -n 40 2>/dev/null | grep -q 'listen tcp :5000'; then
            echo -e "      ${DIM}Hint: Go tried signal on :5000 — panel PORT in .env leaked; run Repair → Repair HTTPS/TLS (#219)${NC}"
        fi
    fi
    if systemctl is-active --quiet "$(console_service_name)" 2>/dev/null; then
        _test_ok "Web console service is active"
    else
        _test_fail "Web console service is NOT active (journalctl -u betterdesk-server)"
    fi

    # ── 1b. Wait for Go signal port (post-restart boot delay) ──
    if systemctl is-active --quiet betterdesk-server 2>/dev/null; then
        if verify_service_health "betterdesk-server" "21116" 15 >/dev/null 2>&1; then
            _test_ok "Go server listening on signal port :21116"
        elif journalctl -u betterdesk-server --no-pager -n 40 2>/dev/null | grep -q 'listen tcp :5000'; then
            _test_fail "Go server tried signal on :5000 (conflicts with panel redirect) — Repair → Repair HTTPS/TLS (#219)"
        else
            _test_fail "Go server not listening on :21116 yet (journalctl -u betterdesk-server)"
        fi
    fi

    # ── 2. Effective runtime configuration (systemd overrides .env) ──
    local https_enabled http_port https_port http_redirect api_tls_mode
    https_enabled=$(read_effective_console_setting HTTPS_ENABLED false)
    http_port=$(resolve_panel_http_port)
    https_port=$(resolve_panel_https_port)
    http_redirect=$(read_effective_console_setting HTTP_REDIRECT_HTTPS true)
    api_tls_mode=$(read_effective_console_setting RUSTDESK_API_TLS auto)

    local panel_scheme="http" panel_port
    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" = "true" ]; then
        panel_scheme="https"
        panel_port="$https_port"
    else
        panel_port="$http_port"
    fi

    # ── 2c. External reverse-proxy mode (TRUST_PROXY + plain HTTP panel) ──
    local trust_proxy_val host_bind
    trust_proxy_val=$(read_effective_console_setting TRUST_PROXY false)
    host_bind=$(read_effective_console_setting HOST "127.0.0.1")
    local _trust_on="no"
    case "$(echo "$trust_proxy_val" | tr '[:upper:]' '[:lower:]')" in
        y|yes|1|true|on) _trust_on="yes" ;;
    esac
    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" != "true" ] && [ "$_trust_on" = "yes" ]; then
        if [ "$host_bind" = "127.0.0.1" ] || [ "$host_bind" = "localhost" ]; then
            _test_ok "Reverse-proxy mode: panel bound to localhost ($host_bind)"
        elif [ "$host_bind" = "0.0.0.0" ]; then
            _test_ok "Reverse-proxy mode: panel bound to all interfaces (remote proxy host)"
        else
            _test_warn "TRUST_PROXY enabled with HOST=$host_bind (use 127.0.0.1 same-host or 0.0.0.0 remote proxy)"
        fi
        if [ -f "$go_svc_file" ] && grep -qE 'Environment=TRUST_PROXY=Y|-trust-proxy' "$go_svc_file" 2>/dev/null; then
            _test_ok "Go server trusts reverse-proxy headers (TRUST_PROXY / -trust-proxy)"
            if grep -qE 'Environment=TRUSTED_PROXIES=.+' "$go_svc_file" 2>/dev/null || \
               grep -qE '^TRUSTED_PROXIES=.+' "${CONSOLE_PATH}/.env" 2>/dev/null; then
                _test_ok "TRUSTED_PROXIES allowlist configured (#276)"
            else
                _test_warn "TRUSTED_PROXIES empty — Go ignores X-Forwarded-* until set (e.g. 127.0.0.1/32)"
            fi
        else
            _test_fail "Go server TRUST_PROXY not enabled — API rate limits may use proxy IP only"
        fi
        local rp_dir="$RUSTDESK_PATH/reverse-proxy"
        if [ -d "$rp_dir" ] && { [ -f "$rp_dir/caddy.Caddyfile.snippet" ] || [ -f "$rp_dir/nginx.betterdesk.conf.snippet" ]; }; then
            _test_ok "Reverse-proxy snippets in $rp_dir/"
        else
            _test_warn "No snippets in $rp_dir/ — re-run SSL menu → External reverse proxy"
        fi
    fi

    # ── 2b. TLS key readable by console user (HTTPS only) ──
    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" = "true" ]; then
        local ssl_key_path console_user="betterdesk"
        ssl_key_path=$(read_effective_console_setting SSL_KEY_PATH "")
        if id "$console_user" &>/dev/null && [ -n "$ssl_key_path" ] && [ -e "$ssl_key_path" ]; then
            if runuser -u "$console_user" -- test -r "$ssl_key_path" 2>/dev/null; then
                _test_ok "TLS private key readable by console user ($console_user)"
            else
                _test_fail "Console user $console_user cannot read TLS key ($ssl_key_path) — HTTPS panel will fall back to HTTP (journalctl -u betterdesk-server)"
            fi
        fi
    fi

    # ── 3. Panel reachability on the correct scheme/port ──
    if systemctl is-active --quiet "$(console_service_name)" 2>/dev/null; then
        verify_service_health "$(console_service_name)" "$panel_port" 15 >/dev/null 2>&1 || true
    fi
    local panel_code panel_insecure="no"
    [ "$panel_scheme" = "https" ] && panel_insecure="yes"
    panel_code=$(_wait_for_http_code "${panel_scheme}://127.0.0.1:${panel_port}/" 15 "$panel_insecure" || true)
    if [[ "$panel_code" =~ ^(200|301|302|304|401|403)$ ]]; then
        _test_ok "Web panel reachable: ${panel_scheme}://<server>:${panel_port} (HTTP $panel_code)"
    else
        local alt_panel_port=""
        if [ "$panel_scheme" = "https" ] && [ "$panel_port" = "443" ] && _tcp_port_is_listening 5443; then
            alt_panel_port="5443"
            panel_code=$(_wait_for_http_code "https://127.0.0.1:5443/" 5 "$panel_insecure" || true)
            if [[ "$panel_code" =~ ^(200|301|302|304|401|403)$ ]]; then
                _test_fail "Web panel NOT reachable on https://127.0.0.1:443 (panel bound :5443 instead — run Repair → Repair permissions for CAP_NET_BIND_SERVICE)"
            else
                _test_fail "Web panel NOT reachable on ${panel_scheme}://127.0.0.1:${panel_port} (got $panel_code)"
            fi
        else
            _test_fail "Web panel NOT reachable on ${panel_scheme}://127.0.0.1:${panel_port} (got $panel_code)"
        fi
        if systemctl is-active --quiet "$(console_service_name)" 2>/dev/null && [ "$panel_scheme" = "https" ]; then
            if journalctl -u betterdesk-server --no-pager -n 80 2>/dev/null | grep -qi 'Falling back to HTTP'; then
                echo -e "      ${DIM}Hint: console logged HTTPS fallback — check TLS key permissions (runuser -u betterdesk test -r key)${NC}"
            elif [ -n "$alt_panel_port" ]; then
                echo -e "      ${DIM}Hint: configured HTTPS_PORT=443 but Node bound :5443 — Repair → Repair permissions, then restart (#219)${NC}"
            fi
        fi
    fi

    # ── 3b. HTTP→HTTPS redirect (only when HTTPS + redirect enabled) ──
    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" = "true" ] \
        && [ "$(echo "$http_redirect" | tr '[:upper:]' '[:lower:]')" = "true" ]; then
        local redirect_hdr _r_elapsed=0 redirect_probe_port="$http_port"
        # Standard HTTPS on :443 always redirects from :80 — never probe stale :5000 (#219).
        if [ "$https_port" = "443" ]; then
            redirect_probe_port="80"
        fi
        redirect_hdr=""
        while [ "$_r_elapsed" -lt 10 ]; do
            redirect_hdr=$(curl -sI --max-time 4 "http://127.0.0.1:${redirect_probe_port}/" 2>/dev/null | grep -i '^location:' | head -1)
            if [ "$https_port" = "443" ]; then
                if echo "$redirect_hdr" | grep -qi 'https://' \
                    && { ! echo "$redirect_hdr" | grep -qiE ':[0-9]+' || echo "$redirect_hdr" | grep -qi ':443'; }; then
                    break
                fi
            elif echo "$redirect_hdr" | grep -qi ":${https_port}"; then
                break
            fi
            sleep 1
            _r_elapsed=$((_r_elapsed + 1))
        done
        if [ "$https_port" = "443" ]; then
            if echo "$redirect_hdr" | grep -qi 'https://' \
                && { ! echo "$redirect_hdr" | grep -qiE ':[0-9]+' || echo "$redirect_hdr" | grep -qi ':443'; }; then
                _test_ok "HTTP redirect active: :${redirect_probe_port} → HTTPS :${https_port}"
            else
                _test_fail "HTTP redirect missing or wrong target on :${redirect_probe_port} (got: ${redirect_hdr:-none})"
                if [ "$redirect_probe_port" = "80" ]; then
                    echo -e "      ${DIM}Hint: set PORT=80 in .env, run Repair → Repair HTTPS/TLS, ensure CAP_NET_BIND_SERVICE (#219)${NC}"
                fi
            fi
        elif echo "$redirect_hdr" | grep -qi ":${https_port}"; then
            _test_ok "HTTP redirect active: :${http_port} → HTTPS :${https_port}"
        else
            _test_fail "HTTP redirect missing or wrong target on :${http_port} (got: ${redirect_hdr:-none})"
        fi
    fi

    # ── 4. Go API (RustDesk client + REST) on GO_API_PORT (default 21114) ──
    local go_api_port="${GO_API_PORT:-21114}"
    local client_api_port="${CLIENT_API_PORT:-21121}"
    local api_code _api_elapsed=0
    while [ "$_api_elapsed" -lt 15 ]; do
        api_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 4 \
            "http://127.0.0.1:${go_api_port}/api/server/stats" 2>/dev/null || echo "000")
        if [[ "$api_code" =~ ^(200|401|403|404)$ ]]; then
            break
        fi
        sleep 1
        _api_elapsed=$((_api_elapsed + 1))
    done
    if [[ "$api_code" =~ ^(200|401|403|404)$ ]]; then
        _test_ok "Go API responding over HTTP on :${go_api_port} (HTTP $api_code)"
    else
        _test_fail "Go API not responding over HTTP on :${go_api_port} (got $api_code)"
        if ! ss -tlnH 2>/dev/null | grep -q ":${go_api_port} "; then
            if ss -tlnpH 2>/dev/null | grep ":${client_api_port} " | grep -qiE 'betterdesk-server|betterdesk-serv'; then
                echo -e "      ${DIM}Hint: Go API bound to :${client_api_port} — API_PORT from .env leaked; run Repair → Repair HTTPS/TLS or update (#219)${NC}"
            fi
        fi
    fi

    # ── 4b. Client API compat proxy (:21121) — scheme matches RUSTDESK_API_TLS ──
    local client_api_scheme="http" client_api_code client_insecure="no"
    if client_api_should_use_tls; then
        client_api_scheme="https"
        client_insecure="yes"
    fi
    client_api_code=$(_wait_for_http_code \
        "${client_api_scheme}://127.0.0.1:${CLIENT_API_PORT:-21121}/api/login-options" 15 "$client_insecure" || true)
    if [[ "$client_api_code" =~ ^(200|401|403|404|405)$ ]]; then
        _test_ok "Client API compat proxy on :${CLIENT_API_PORT:-21121} (${client_api_scheme^^} $client_api_code)"
    else
        _test_fail "Client API proxy not responding on :${CLIENT_API_PORT:-21121} (${client_api_scheme}, got $client_api_code) — check API_ENABLED / RUSTDESK_API_TLS"
    fi

    # Critical invariant: Go API must never be HTTPS-only
    if [ -f "$go_svc_file" ] && grep -Eq '\-tls-api|\-force-https' "$go_svc_file" 2>/dev/null; then
        _test_warn "Go service carries -tls-api/-force-https — RustDesk clients require plain HTTP on :${GO_API_PORT:-21114}"
    fi

    # ── 5. Signal / Relay listeners ──
    local p
    for p in 21116 21117; do
        if ss -tlnH 2>/dev/null | grep -q ":${p} "; then
            _test_ok "Listener present on TCP :${p}"
        else
            _test_warn "No TCP listener detected on :${p} (UDP-only signal is normal for :21116)"
        fi
    done

    # ── 6. Certificate validation (HTTPS / TLS modes) ──
    local tls_active="no"
    [ -f "$go_svc_file" ] && grep -q '\-tls-signal' "$go_svc_file" 2>/dev/null && tls_active="yes"
    if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" = "true" ] || [ "$tls_active" = "yes" ]; then
        if [ -f "$ssl_dir/betterdesk.crt" ]; then
            if openssl x509 -in "$ssl_dir/betterdesk.crt" -noout 2>/dev/null; then
                local not_after days_left
                not_after=$(openssl x509 -in "$ssl_dir/betterdesk.crt" -noout -enddate 2>/dev/null | cut -d= -f2)
                if [ -n "$not_after" ]; then
                    local exp_epoch now_epoch
                    exp_epoch=$(date -d "$not_after" +%s 2>/dev/null || echo 0)
                    now_epoch=$(date +%s)
                    if [ "$exp_epoch" -gt "$now_epoch" ]; then
                        days_left=$(( (exp_epoch - now_epoch) / 86400 ))
                        if [ "$days_left" -lt 14 ]; then
                            _test_warn "Certificate valid but expires in ${days_left} days ($not_after)"
                        else
                            _test_ok "Certificate valid for ${days_left} more days (until $not_after)"
                        fi
                    else
                        _test_fail "Certificate has EXPIRED ($not_after)"
                    fi
                fi
                local san
                san=$(openssl x509 -in "$ssl_dir/betterdesk.crt" -noout -ext subjectAltName 2>/dev/null | tail -n +2 | tr -d ' ')
                [ -n "$san" ] && echo -e "      ${DIM}SAN: ${san}${NC}"
            else
                _test_fail "Certificate file is not a valid X.509 certificate"
            fi
        else
            _test_fail "HTTPS/TLS enabled but no certificate found at $ssl_dir/betterdesk.crt"
        fi

        if [ "$tls_active" = "yes" ]; then
            if echo | timeout 5 openssl s_client -connect "127.0.0.1:21116" 2>/dev/null | grep -q 'BEGIN CERTIFICATE'; then
                _test_ok "TLS handshake succeeded on signal :21116"
            else
                _test_warn "Could not complete TLS handshake on :21116 (dual-mode listener may still accept plain TCP)"
            fi
        fi
    fi

    echo ""
    local go_signal_port relay_port
    go_signal_port=$(grep -m1 '^Environment=SIGNAL_PORT=' "$go_svc_file" 2>/dev/null | cut -d= -f2- || echo "21116")
    relay_port=$(grep -m1 '^Environment=RELAY_PORT=' "$go_svc_file" 2>/dev/null | cut -d= -f2- || echo "21117")
    echo -e "  ${DIM}Effective config: HTTPS_ENABLED=${https_enabled} panel=${panel_scheme}:${panel_port} redirect=${http_redirect} client_api_tls=${api_tls_mode} go_signal=${go_signal_port} go_relay=${relay_port}${NC}"
    echo ""
    echo -e "  ${GREEN}${pass} passed${NC}   ${YELLOW}${warn} warnings${NC}   ${RED}${fail} failed${NC}"
    if [ "$fail" -gt 0 ]; then
        echo -e "  ${YELLOW}Some checks failed — review the messages above and the service logs.${NC}"
    else
        echo -e "  ${GREEN}Configuration verified successfully.${NC}"
        if [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" = "true" ] && [ "$https_port" = "5443" ]; then
            echo ""
            echo -e "  ${DIM}Tip: for https://your-domain without :5443, set HTTPS_PORT=443 in .env and run Repair → Repair permissions, or re-run Protocol Toggle / SSL config and choose standard port 443. See docs/setup/HTTPS_SETUP.md${NC}"
        elif [ "$(echo "$https_enabled" | tr '[:upper:]' '[:lower:]')" != "true" ] && [ "$_trust_on" = "yes" ]; then
            echo ""
            echo -e "  ${DIM}Tip: configure Caddy/Nginx using $RUSTDESK_PATH/reverse-proxy/ snippets, then open https://your-domain/ (not :5443). See docs/setup/REVERSE_PROXY.md${NC}"
        fi
    fi
    echo ""

    unset -f _test_ok _test_fail _test_warn 2>/dev/null || true
}

#===============================================================================
# HTTP/HTTPS Protocol Toggle
#===============================================================================

do_toggle_protocol() {
    maybe_reexec_if_installer_on_disk_is_newer
    print_header
    echo -e "${WHITE}${BOLD}══════════ PROTOCOL TOGGLE (HTTP / HTTPS) ══════════${NC}"
    echo ""

    local env_file="$CONSOLE_PATH/.env"
    local svc_file="/etc/systemd/system/betterdesk-console.service"
    local go_svc_file="$SERVER_UNIT"
    local ssl_dir="$RUSTDESK_PATH/ssl"

    # Detect current mode (effective runtime: systemd overrides .env)
    local current_mode="HTTP"
    if [ "$(read_effective_console_setting HTTPS_ENABLED false | tr '[:upper:]' '[:lower:]')" = "true" ]; then
        current_mode="HTTPS"
    fi

    local tls_signal="no"
    local tls_relay="no"
    if [ -f "$go_svc_file" ]; then
        grep -q '\-tls-signal' "$go_svc_file" 2>/dev/null && tls_signal="yes"
        grep -q '\-tls-relay' "$go_svc_file" 2>/dev/null && tls_relay="yes"
    fi

    local _menu_items=(
        $'Switch to HTTP\tEverything plain — LAN / testing'
        $'Switch to HTTPS\tPanel HTTPS + signal/relay TLS'
        $'External reverse proxy\tTLS at Caddy/Nginx — panel HTTP on localhost'
        $'Back\tReturn to the main menu'
    )
    local _menu_returns=( 1 2 3 0 )
    menu_choose "Protocol Toggle (HTTP / HTTPS / reverse proxy)" "Current: ${current_mode} | signal TLS: ${tls_signal} | relay TLS: ${tls_relay}"
    local proto_choice="$MENU_CHOICE"

    case "${proto_choice:-0}" in
        1)
            # ── Switch to HTTP ──
            echo ""
            print_step "Switching to HTTP mode..."

            apply_console_protocol_mode http
            clear_go_server_signal_relay_tls

            print_success "Switched to HTTP mode"
            echo ""
            print_info "  Panel:         HTTP :$(resolve_panel_http_port)"
            print_info "  Signal:        TCP  :21116"
            print_info "  Relay:         TCP  :21117"
            print_info "  Go API:        HTTP :${GO_API_PORT:-21114}"
            print_info "  Client API:    HTTP :${CLIENT_API_PORT:-21121}"
            echo ""
            print_warning "SSL certificates were NOT deleted (use option C > 4 to remove)"
            print_info "If your browser still redirects to HTTPS, clear site cache or HSTS (chrome://net-internals/#hsts)"
            ;;
        2)
            # ── Switch to HTTPS ──
            echo ""
            local have_cert="no"
            [ -f "$ssl_dir/betterdesk.crt" ] && [ -f "$ssl_dir/betterdesk.key" ] && have_cert="yes"
            local _keep_desc="No existing certificate found"
            [ "$have_cert" = "yes" ] && _keep_desc="Reuse $ssl_dir/betterdesk.crt"
            local _menu_items=(
                $'Keep existing certificate\t'"$_keep_desc"
                $'Self-signed certificate\tGenerate one for LAN / testing'
                $'Let\'s Encrypt certificate\tPublic domain, port 80 must be free'
                $'Custom certificate\tPaste your own cert + key file paths'
                $'Cancel\tDo not change the protocol'
            )
            local _menu_returns=( 1 2 3 4 0 )
            menu_choose "HTTPS Certificate Source" "Choose the certificate to use for TLS"
            local cert_choice="$MENU_CHOICE"

            case "${cert_choice:-2}" in
                1)
                    if [ "$have_cert" != "yes" ]; then
                        print_error "No existing certificate found — choose another option."
                        press_enter
                        return
                    fi
                    maybe_repair_le_ssl_symlinks 2>/dev/null || true
                    print_info "Using existing certificate at $ssl_dir/betterdesk.crt"
                    ;;
                2)
                    mkdir -p "$ssl_dir"
                    local server_ip lan_ip san_list
                    server_ip=$(get_public_ip 2>/dev/null || echo "127.0.0.1")
                    lan_ip=$(ip route get 1 2>/dev/null | awk '{print $7; exit}')
                    san_list="IP:$server_ip,IP:127.0.0.1,DNS:localhost"
                    [ -n "$lan_ip" ] && [ "$lan_ip" != "$server_ip" ] && san_list="$san_list,IP:$lan_ip"
                    read -p "Optional DNS domain for the certificate (blank to skip): " ss_domain
                    [ -n "$ss_domain" ] && san_list="$san_list,DNS:$ss_domain"
                    print_step "Generating self-signed certificate..."
                    openssl req -x509 -nodes -days 3650 -newkey rsa:4096 \
                        -keyout "$ssl_dir/betterdesk.key" \
                        -out "$ssl_dir/betterdesk.crt" \
                        -subj "/CN=${ss_domain:-$server_ip}/O=BetterDesk/C=PL" \
                        -addext "subjectAltName=$san_list" 2>/dev/null || \
                    openssl req -x509 -nodes -days 3650 -newkey rsa:4096 \
                        -keyout "$ssl_dir/betterdesk.key" \
                        -out "$ssl_dir/betterdesk.crt" \
                        -subj "/CN=${ss_domain:-$server_ip}/O=BetterDesk/C=PL" 2>/dev/null
                    if ! deploy_ssl_material_to_rustdesk_dir "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key"; then
                        print_error "Failed to set permissions on self-signed certificate"
                        press_enter
                        return
                    fi
                    print_success "Self-signed certificate generated"
                    ;;
                3)
                    if ! command -v certbot &>/dev/null; then
                        print_step "Installing certbot..."
                        if command -v dnf &>/dev/null; then dnf install -y certbot &>/dev/null
                        elif command -v apt-get &>/dev/null; then apt-get install -y certbot &>/dev/null
                        elif command -v yum &>/dev/null; then yum install -y certbot &>/dev/null; fi
                    fi
                    if ! command -v certbot &>/dev/null; then
                        print_error "certbot could not be installed automatically."
                        press_enter
                        return
                    fi
                    read -p "Public domain (e.g. desk.example.com): " le_domain
                    read -p "Admin email (for renewal notices): " le_email
                    if [ -z "$le_domain" ]; then
                        print_error "A domain is required for Let's Encrypt."
                        press_enter
                        return
                    fi
                    print_step "Requesting certificate for $le_domain (standalone, needs port 80)..."
                    if certbot certonly --standalone --non-interactive --agree-tos \
                        ${le_email:+--email "$le_email"} ${le_email:+} \
                        $([ -z "$le_email" ] && echo "--register-unsafely-without-email") \
                        -d "$le_domain"; then
                        local le_live_dir="/etc/letsencrypt/live/$le_domain"
                        if ! deploy_ssl_material_to_rustdesk_dir \
                            "$le_live_dir/fullchain.pem" "$le_live_dir/privkey.pem" "$le_live_dir"; then
                            print_error "Failed to deploy Let's Encrypt certificate for console user"
                            press_enter
                            return
                        fi
                        print_success "Let's Encrypt certificate installed for $le_domain"
                        print_info "Open the panel at https://${le_domain}:$(resolve_panel_https_port) (IP access will show certificate errors)"
                    else
                        print_error "certbot failed — check that DNS points here and port 80 is free."
                        press_enter
                        return
                    fi
                    ;;
                4)
                    read -p "Path to certificate (.crt/.pem, fullchain): " custom_crt
                    read -p "Path to private key (.key): " custom_key
                    read -p "Path to CA chain (optional, blank to skip): " custom_ca
                    if [ ! -f "$custom_crt" ] || [ ! -f "$custom_key" ]; then
                        print_error "Certificate or key file not found."
                        press_enter
                        return
                    fi
                    if ! openssl x509 -in "$custom_crt" -noout 2>/dev/null; then
                        print_error "The provided certificate is not a valid X.509 file."
                        press_enter
                        return
                    fi
                    local deploy_crt="$custom_crt" merged_crt=""
                    if [ -n "$custom_ca" ] && [ -f "$custom_ca" ]; then
                        merged_crt=$(mktemp)
                        cat "$custom_crt" "$custom_ca" > "$merged_crt"
                        deploy_crt="$merged_crt"
                    fi
                    if ! deploy_ssl_material_to_rustdesk_dir "$deploy_crt" "$custom_key"; then
                        [ -n "$merged_crt" ] && rm -f "$merged_crt"
                        print_error "Failed to deploy custom certificate for console user"
                        press_enter
                        return
                    fi
                    [ -n "$merged_crt" ] && rm -f "$merged_crt"
                    print_success "Custom certificate installed"
                    ;;
                0|*)
                    print_info "Cancelled — no changes made."
                    press_enter
                    return
                    ;;
            esac

            local api_tls="false" allow_self_signed="true"
            case "${cert_choice:-2}" in
                1)
                    infer_tls_mode_from_cert "$ssl_dir/betterdesk.crt"
                    api_tls="$INFERRED_RUSTDESK_API_TLS"
                    allow_self_signed="$INFERRED_ALLOW_SELF_SIGNED"
                    ;;
                2)
                    api_tls="false"
                    allow_self_signed="true"
                    ;;
                3)
                    api_tls="true"
                    allow_self_signed="false"
                    ;;
                4)
                    infer_tls_mode_from_cert "$ssl_dir/betterdesk.crt"
                    api_tls="$INFERRED_RUSTDESK_API_TLS"
                    allow_self_signed="$INFERRED_ALLOW_SELF_SIGNED"
                    ;;
            esac

            print_step "Switching to HTTPS mode..."

            apply_console_protocol_mode https "$ssl_dir/betterdesk.crt" "$ssl_dir/betterdesk.key" "$api_tls" "$allow_self_signed"

            sync_go_server_signal_relay_tls "$ssl_dir"
            ensure_service_user >/dev/null

            print_success "Switched to HTTPS mode"
            echo ""
            print_info "  Panel:         HTTPS :$(resolve_panel_https_port)"
            print_info "  Redirect:      HTTP :$(resolve_panel_http_port) → HTTPS :$(resolve_panel_https_port)"
            print_info "  Signal:        TLS   :21116"
            print_info "  Relay:         TLS   :21117"
            print_info "  Go API:        HTTP  :${GO_API_PORT:-21114} (RustDesk client + REST)"
            if [ "$api_tls" = "true" ]; then
                print_info "  Client API:    HTTPS :${CLIENT_API_PORT:-21121}"
            else
                print_info "  Client API:    HTTP  :${CLIENT_API_PORT:-21121}"
            fi
            maybe_offer_standard_https_port
            ;;
        3)
            do_configure_reverse_proxy || true
            ;;
        0|4|*)
            return
            ;;
    esac

    echo ""
    if confirm "Restart BetterDesk services now?"; then
        repair_https_stuck_state yes
        ensure_console_tls_material_readable 2>/dev/null || true
        systemctl restart betterdesk-server 2>/dev/null || true
        verify_service_health "betterdesk-server" "21116" 15 >/dev/null 2>&1 || true
        verify_go_install 60 >/dev/null 2>&1 || true
        print_success "BetterDesk services restarted"
        run_protocol_tests
    else
        print_info "Changes saved. Restart later with: systemctl restart betterdesk-server"
    fi

    press_enter
}

#===============================================================================
# Database Migration Functions
#===============================================================================

# migrate_dst_overwrite DST: whether the migration tool may replace the
# SQLite file at DST. Sets MIGRATE_OVERWRITE to -overwrite when the file
# (or a -wal, -shm or -journal of one) exists and the user agreed, to
# nothing when there is nothing to replace; returns 1 when the user
# declined. A postgres:// destination needs nothing.
migrate_dst_overwrite() {
    local dst="$1"
    MIGRATE_OVERWRITE=""
    case "${dst,,}" in
        postgres://*|postgresql://*) return 0 ;;
    esac
    if [ -e "$dst" ] || [ -e "$dst-wal" ] || [ -e "$dst-shm" ] || [ -e "$dst-journal" ]; then
        print_warning "Destination file exists: $dst"
        confirm "Overwrite (backup will be created first)?" || return 1
        MIGRATE_OVERWRITE="-overwrite"
    fi
}

# migrate_sqlite_dst_done DST: hands a SQLite file the migration tool wrote
# to the service account, as the install does with the server's files.
migrate_sqlite_dst_done() {
    case "${1,,}" in
        postgres://*|postgresql://*) return 0 ;;
    esac
    own_by_service_user "$1"
}

do_migrate_database() {
    print_header
    echo -e "${WHITE}${BOLD}══════════ DATABASE MIGRATION ══════════${NC}"
    echo ""

    # Locate migration binary
    local migrate_bin=""
    local arch=$(uname -m)
    local search_paths=(
        "$SCRIPT_DIR/betterdesk-server/tools/migrate/migrate-linux-amd64"
        "$SCRIPT_DIR/tools/migrate/migrate-linux-amd64"
        "$RUSTDESK_PATH/migrate"
        "/usr/local/bin/betterdesk-migrate"
    )

    for p in "${search_paths[@]}"; do
        if [ -f "$p" ] && [ -x "$p" ]; then
            migrate_bin="$p"
            break
        fi
    done

    if [ -z "$migrate_bin" ]; then
        # Try to find non-executable and make it executable
        for p in "${search_paths[@]}"; do
            if [ -f "$p" ]; then
                chmod +x "$p"
                migrate_bin="$p"
                break
            fi
        done
    fi

    if [ -z "$migrate_bin" ]; then
        print_error "Migration binary not found!"
        print_info "Expected at: $SCRIPT_DIR/betterdesk-server/tools/migrate/migrate-linux-amd64"
        print_info "Build it with: cd betterdesk-server && go build -o tools/migrate/migrate-linux-amd64 ./tools/migrate/"
        press_enter
        return
    fi

    print_info "Migration binary: $migrate_bin"
    echo ""
    local _menu_items=(
        $'Rust -> Go\tMigrate legacy Rust hbbs database to the Go server'
        $'SQLite -> PostgreSQL\tMove the whole install (every server and console table) to PostgreSQL'
        $'PostgreSQL -> SQLite\tMove the whole install from PostgreSQL to a SQLite file'
        $'Backup\tCreate a timestamped SQLite database backup'
        $'Back\tReturn to the main menu'
    )
    local _menu_returns=( 1 2 3 4 0 )
    menu_choose "Database Migration" "Migrate databases between BetterDesk components"
    local mig_choice="$MENU_CHOICE"

    case $mig_choice in
        1)
            # Rust → Go
            echo ""
            local default_src="$RUSTDESK_PATH/db_v2.sqlite3"
            read -p "Source Rust database [$default_src]: " src_db
            src_db="${src_db:-$default_src}"

            if [ ! -f "$src_db" ]; then
                print_error "Source database not found: $src_db"
                press_enter
                return
            fi

            read -p "Destination (SQLite path or postgres:// URI) [new file next to source]: " dst_db
            dst_db="${dst_db:-$(dirname "$src_db")/betterdesk.sqlite3}"
            if ! migrate_dst_overwrite "$dst_db"; then
                press_enter
                return
            fi

            print_step "Creating backup before migration..."
            "$migrate_bin" -mode backup -src "$src_db" 2>&1 || true
            if [ -n "$MIGRATE_OVERWRITE" ] && [ -f "$dst_db" ]; then
                "$migrate_bin" -mode backup -src "$dst_db" 2>&1 || true
            fi

            print_step "Running Rust → Go migration..."
            if "$migrate_bin" -mode rust2go -src "$src_db" -dst "$dst_db" ${MIGRATE_OVERWRITE:+"$MIGRATE_OVERWRITE"} 2>&1; then
                migrate_sqlite_dst_done "$dst_db"
                print_success "Rust → Go migration completed successfully!"
            else
                print_error "Migration failed. Check the output above for details."
            fi
            ;;
        2)
            # SQLite → PostgreSQL
            echo ""
            local default_src="$RUSTDESK_PATH/db_v2.sqlite3"
            read -p "Source SQLite database [$default_src]: " src_db
            src_db="${src_db:-$default_src}"

            if [ ! -f "$src_db" ]; then
                print_error "Source database not found: $src_db"
                press_enter
                return
            fi

            read -p "PostgreSQL connection URI (postgres://user:pass@host:5432/dbname): " pg_uri
            if [ -z "$pg_uri" ]; then
                print_error "PostgreSQL URI is required"
                press_enter
                return
            fi

            print_info "Every table of the server and the console moves: devices, users, settings, tickets, groups, audit log and the rest."
            local pg_overwrite=""
            if confirm "If the PostgreSQL database already holds data, replace all of it?"; then
                pg_overwrite="-overwrite"
            fi

            print_step "Creating backup before migration..."
            "$migrate_bin" -mode backup -src "$src_db" 2>&1 || true

            print_step "Running SQLite → PostgreSQL migration..."
            "$migrate_bin" -mode sqlite2pg -src "$src_db" -dst "$pg_uri" ${pg_overwrite:+"$pg_overwrite"} 2>&1

            if [ $? -eq 0 ]; then
                print_success "SQLite → PostgreSQL migration completed successfully!"
                print_info "Update your BetterDesk Go server config: DB_URL=$pg_uri"
            else
                print_error "Migration failed. Check the output above for details."
            fi
            ;;
        3)
            # PostgreSQL → SQLite
            echo ""
            read -p "PostgreSQL connection URI (postgres://user:pass@host:5432/dbname): " pg_uri
            if [ -z "$pg_uri" ]; then
                print_error "PostgreSQL URI is required"
                press_enter
                return
            fi

            local default_dst="$RUSTDESK_PATH/db_v2.sqlite3"
            read -p "Destination SQLite file [$default_dst]: " dst_db
            dst_db="${dst_db:-$default_dst}"

            print_info "Every table of the server and the console moves: devices, users, settings, tickets, groups, audit log and the rest."
            if ! migrate_dst_overwrite "$dst_db"; then
                press_enter
                return
            fi
            if [ -n "$MIGRATE_OVERWRITE" ] && [ -f "$dst_db" ]; then
                "$migrate_bin" -mode backup -src "$dst_db" 2>&1 || true
            fi

            print_step "Running PostgreSQL → SQLite migration..."
            if "$migrate_bin" -mode pg2sqlite -src "$pg_uri" -dst "$dst_db" ${MIGRATE_OVERWRITE:+"$MIGRATE_OVERWRITE"} 2>&1; then
                migrate_sqlite_dst_done "$dst_db"
                print_success "PostgreSQL → SQLite migration completed successfully!"
            else
                print_error "Migration failed. Check the output above for details."
            fi
            ;;
        4)
            # Backup
            echo ""
            local default_src="$RUSTDESK_PATH/db_v2.sqlite3"
            read -p "SQLite database to backup [$default_src]: " src_db
            src_db="${src_db:-$default_src}"

            if [ ! -f "$src_db" ]; then
                print_error "Database not found: $src_db"
                press_enter
                return
            fi

            print_step "Creating backup..."
            "$migrate_bin" -mode backup -src "$src_db" 2>&1

            if [ $? -eq 0 ]; then
                print_success "Backup created successfully!"
            else
                print_error "Backup failed."
            fi
            ;;
        0)
            return
            ;;
        *)
            print_warning "Invalid option"
            ;;
    esac

    press_enter
}

#===============================================================================
# Main Menu
#===============================================================================

show_menu() {
    print_header
    print_status
    
    echo -e "${WHITE}${BOLD}══════════ MAIN MENU ══════════${NC}"
    echo ""
    echo "  1. 🚀 FRESH INSTALLATION"
    echo "  2. ⬆️  UPDATE"
    echo "  3. 🔧 REPAIR INSTALLATION"
    echo "  4. ✅ INSTALLATION VALIDATION"
    echo "  5. 💾 Backup"
    echo "  6. 🔐 Reset admin password"
    echo "  7. 🔨 Build & deploy server"
    echo "  8. 📊 DIAGNOSTICS"
    echo "  9. 🗑️  UNINSTALL"
    echo ""
    echo "  L. 📦 MINIMAL INSTALLATION (server only)"
    echo "  C. 🔒 Configure SSL certificates"
    echo "  T. 🔄 Toggle HTTP/HTTPS mode"
    echo "  M. 🔄 Database migration"
    echo "  B. 🧰 Build toolchain (optional/dev — not needed for production)"
    echo "  S. ⚙️  Settings (paths)"
    echo "  P. 🛡️  Management capabilities and server settings"
    echo "  R. ↩️  Roll back the upgrade (Node.js console back)"
    echo "  0. ❌ Exit"
    echo ""
    echo -e "  ${DIM}Tip: this menu also supports arrow-key navigation (set BETTERDESK_CLASSIC_MENU=1 to force this list).${NC}"
    echo ""
}

main() {
    # Check root
    if [ "$EUID" -ne 0 ]; then
        echo -e "${RED}This script requires root privileges!${NC}"
        echo "Run: sudo $0"
        exit 1
    fi
    
    # Auto-detect paths on startup
    echo -e "${CYAN}Detecting installation...${NC}"
    auto_detect_paths
    echo ""
    sleep 1

    default_to_auto_without_terminal

    if [ "$CHECK_CAPABILITIES" = true ] || [ "$REPAIR_PERMISSIONS" = true ] \
        || [ "${#MANAGED_CONFIG_CHANGES[@]}" -gt 0 ]; then
        local management_rc=0
        [ "$REPAIR_PERMISSIONS" = true ] && repair_management_permissions
        check_management_capabilities || management_rc=$?
        if [ "${#MANAGED_CONFIG_CHANGES[@]}" -gt 0 ]; then
            apply_managed_config || management_rc=$?
        fi
        exit "$management_rc"
    fi

    # Update, rollback and cleanup run without questions
    if [ "$ROLLBACK_GO_MODE" = true ]; then
        AUTO_MODE=true
        rollback_go_upgrade
        exit $?
    fi
    if [ "$CLEANUP_NODE_MODE" = true ]; then
        AUTO_MODE=true
        cleanup_node_leftovers
        exit $?
    fi
    if [ "$UPDATE_MODE" = true ]; then
        AUTO_MODE=true
        do_update
        exit $?
    fi

    # Auto mode - run installation directly; an install with the Node.js
    # console is upgraded in place
    if [ "$AUTO_MODE" = true ]; then
        print_info "Running in AUTO mode..."
        if [ "$UNINSTALL_MODE" = true ]; then
            do_uninstall
        elif is_node_era_install; then
            do_update
        elif [ "$MINIMAL_MODE" = true ]; then
            do_install_minimal
        else
            do_install
        fi
        exit $?
    fi

    # Action tokens map 1:1 to the classic case dispatch below, so both the
    # arrow-key TUI and the numeric fallback share the exact same handlers.
    local menu_labels=(
        $'Fresh installation\tFull install from scratch'
        $'Update\tNewest release; a Node.js console install is upgraded in place'
        $'Repair installation\tFix common problems'
        $'Validate installation\tCheck correctness'
        $'Backup\tCreate a backup'
        $'Reset admin password\tReset the console admin'
        $'Build & deploy server\tCompile and deploy the Go server'
        $'Diagnostics\tDetailed problem analysis'
        $'Uninstall\tRemove BetterDesk'
        $'Minimal installation\tServer with its web console off'
        $'Configure SSL certificates\tLet'"'"'s Encrypt / custom / self-signed'
        $'Toggle HTTP/HTTPS\tSwitch protocol + run tests'
        $'Database migration\tMigrate between backends'
        $'Build toolchain (optional/dev)\tLocal Flutter/Cargo — not needed for production'
        $'Settings (paths)\tConfigure install paths'
        $'Management capabilities\tCheck permissions and change allowlisted server settings'
        $'Roll back the upgrade\tPut the Node.js console and old server back'
        $'Exit\tQuit the manager'
    )
    local menu_actions=( 1 2 3 4 5 6 7 8 9 L C T M B S P R 0 )

    while true; do
        local choice=""
        if tui_available; then
            detect_installation 2>/dev/null
            local status_line="Install: ${INSTALL_STATUS:-unknown}"
            [ "$HBBS_RUNNING" = true ] && status_line="$status_line  |  server: running" || status_line="$status_line  |  server: stopped"
            [ "$CONSOLE_RUNNING" = true ] && status_line="$status_line  |  console: running" || status_line="$status_line  |  console: stopped"
            if tui_select "BetterDesk Console Manager v${VERSION}" "$status_line" "${menu_labels[@]}"; then
                choice="${menu_actions[$TUI_RESULT]}"
            else
                choice="0"
            fi
        else
            show_menu
            read -p "Select option: " choice
        fi

        case $choice in
            1) do_install ;;
            2) do_update ;;
            3) do_repair ;;
            4) do_validate ;;
            5) do_backup ;;
            6) do_reset_password ;;
            7) do_build ;;
            8) do_diagnostics ;;
            9) do_uninstall ;;
            [Ll]) do_install_minimal ;;
            [Cc]) do_configure_ssl ;;
            [Tt]) do_toggle_protocol ;;
            [Mm]) do_migrate_database ;;
            [Bb]) do_install_build_toolchain ;;
            [Ss]) configure_paths ;;
            [Pp]) configure_management ;;
            [Rr]) rollback_go_upgrade; press_enter ;;
            0) 
                echo ""
                print_info "Goodbye!"
                exit 0
                ;;
            *)
                print_warning "Invalid option"
                sleep 1
                ;;
        esac
    done
}

# Run only when executed directly or piped into bash (curl ... | sudo bash,
# where BASH_SOURCE is empty); this also lets installer tests source the
# bounded command helpers without starting the interactive manager.
if [[ "${BASH_SOURCE[0]:-$0}" == "$0" ]]; then
    main "$@"
fi
