#!/bin/bash
#===============================================================================
#
#   NeoDesk Console Manager v3.6.2 (Docker)
#
#   Installs, updates and removes NeoDesk on Docker: one container that
#   runs the published image ghcr.io/neodeskapp/neodesk (signal,
#   relay, API and the web console), with docker-compose.yml and .env in
#   one folder (/opt/neodesk/docker).
#
#   sudo bash neodesk-docker.sh            install, or update an install
#   sudo bash neodesk-docker.sh --help     every command and option
#
#===============================================================================

if [ -z "${BASH_VERSION:-}" ]; then
    echo "Run this script with bash: sudo bash $0" >&2
    exit 1
fi

set -Eeuo pipefail

VERSION="3.6.2"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || pwd)"

# Settings: flags win over these variables, which win over the defaults
NEODESK_DIR="${NEODESK_DIR:-/opt/neodesk/docker}"
# The image the compose file runs (a mirror goes into docker-compose.override.yml)
NEODESK_IMAGE="ghcr.io/neodeskapp/neodesk"
NEODESK_IMAGE_TAG_SET="${NEODESK_IMAGE_TAG:-}"
NEODESK_RAW_BASE="${NEODESK_RAW_BASE:-https://raw.githubusercontent.com/neodeskapp/neodesk/${NEODESK_BRANCH:-main}}"
RELAY_SERVERS_SET="${RELAY_SERVERS:-}"
RELAY_MODE="${RELAY_MODE:-auto}"
CONSOLE_PORT_SET="${NEODESK_CONSOLE_PORT:-}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
USE_POSTGRES="${NEODESK_POSTGRES:-false}"
# HTTPS with Let's Encrypt: Traefik in front of the console (the compose
# file's profile "traefik"). A domain given turns it on, --no-traefik off.
DOMAIN_SET="${NEODESK_DOMAIN:-}"
LETSENCRYPT_EMAIL_SET="${LETSENCRYPT_EMAIL:-}"
LETSENCRYPT_STAGING="${LETSENCRYPT_STAGING:-false}"
TRAEFIK_ACTION=""
LETSENCRYPT_STAGING_CA="https://acme-staging-v02.api.letsencrypt.org/directory"
CERT_TIMEOUT="${NEODESK_CERT_TIMEOUT:-120}"

COMMAND=""
ASSUME_YES=false
PURGE=false
NO_PULL=false
BUILD_LOCAL=false
INSTALL_DOCKER=true
ADOPT=false
RESET_USER="admin"
MOVE_DATA=""
OVERWRITE_POSTGRES=false
MANAGED_CONFIG_CHANGES=()

CONTAINER="neodesk"
PG_CONTAINER="neodesk-postgres"
TRAEFIK_CONTAINER="neodesk-traefik"
DATA_VOLUME="neodesk-data"
CONSOLE_VOLUME="neodesk-console-data"
PG_VOLUME="neodesk-postgres-data"
TRAEFIK_VOLUME="neodesk-traefik-letsencrypt"
OLD_CONTAINERS=(neodesk neodesk-server neodesk-console)
HEALTH_TIMEOUT="${NEODESK_HEALTH_TIMEOUT:-180}"
KEEP_BACKUPS=5
DOCKER_PORTS_TCP=(21115 21116 21117 21118 21119 21121 21122)
DOCKER_PORTS_UDP=(21116)

# Colours: 24-bit where the terminal says it has them (COLORTERM), else the
# 16 basic ones; none when the output is not a terminal, NO_COLOR is set or
# TERM is dumb (COLOR_DEPTH 24bit, 16 or empty). In 24-bit, RED, GREEN,
# YELLOW and CYAN keep at least 3.8:1 on black, on PowerShell's blue and on
# white.
COLOR_DEPTH=""
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-}" != dumb ]; then
    case "${COLORTERM:-}" in
        truecolor|24bit) COLOR_DEPTH=24bit ;;
        *) COLOR_DEPTH=16 ;;
    esac
fi
case "$COLOR_DEPTH" in
    24bit)
        RED=$'\033[38;2;228;75;69m' GREEN=$'\033[38;2;37;149;46m' YELLOW=$'\033[38;2;178;116;9m'
        CYAN=$'\033[38;2;26;143;147m' BOLD=$'\033[1m' NC=$'\033[0m'
        ;;
    16)
        RED=$'\033[0;31m' GREEN=$'\033[0;32m' YELLOW=$'\033[0;33m' CYAN=$'\033[0;36m' BOLD=$'\033[1m' NC=$'\033[0m'
        ;;
    *)
        RED="" GREEN="" YELLOW="" CYAN="" BOLD="" NC=""
        ;;
esac

say() { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$CYAN" "$NC" "$*"; }
ok() { printf '%sOK%s  %s\n' "$GREEN" "$NC" "$*"; }
warn() { printf '%sWARNING:%s %s\n' "$YELLOW" "$NC" "$*" >&2; }
# die MESSAGE [WHAT TO DO...]: the error and, one line each, what to do
die() {
    printf '%sERROR:%s %s\n' "$RED" "$NC" "$1" >&2
    shift
    local line
    for line in "$@"; do printf '       %s\n' "$line" >&2; done
    exit 1
}

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
# and its version; one plain line without colour (no terminal, NO_COLOR).
# install.sh shows its own before it runs this script
# (NEODESK_HEADER_SHOWN=1), and this one stays out then.
print_header() {
    local title="Docker installer ${VERSION}"
    [ "${NEODESK_HEADER_SHOWN:-}" != 1 ] || return 0
    if [ -z "$COLOR_DEPTH" ]; then
        say "NeoDesk $title"
        return 0
    fi
    say ""
    draw_logo "$title"
    say ""
}

usage() {
    cat <<EOF
NeoDesk on Docker, manager v${VERSION}

Usage: sudo bash neodesk-docker.sh [command] [options]

Commands:
  install          Install NeoDesk, or update the install (the default)
  update           Update the install: back up the data, pull the image,
                   start it, and go back to the old image when the new
                   container does not get healthy
  status           Containers, health, version and the addresses for clients
  logs             Follow the container's log
  backup           Back up the data volumes into <folder>/backups
  reset-password   Give an account a new random password (printed once)
  postgres         Move the install from SQLite to PostgreSQL
  uninstall        Stop and remove the containers; volumes, images and the
                   folder stay unless --purge
  menu             The same commands as a menu

Options:
  --dir PATH             Install folder (NEODESK_DIR, /opt/neodesk/docker)
  --tag TAG              Image tag (NEODESK_IMAGE_TAG); latest, the default,
                         is the newest release, a version such as 3.5.270
                         pins one
  --relay ADDRESS        The address clients reach this server at, public IP
                         or DNS name (RELAY_SERVERS); found by itself when empty
  --relay-mode MODE      How to find it: auto, public or local (RELAY_MODE)
  --console-port PORT    Host port of the web console (NEODESK_CONSOLE_PORT,
                         5000)
  --postgres             A new install on PostgreSQL instead of SQLite
                         (NEODESK_POSTGRES=true)
  --domain NAME          HTTPS for the console at this DNS name (NEODESK_DOMAIN):
                         a Traefik container gets a Let's Encrypt certificate
                         and renews it by itself. The name must point at this
                         server, and ports 80 and 443 must reach it.
  --email ADDRESS        Let's Encrypt's contact for expiry notices
                         (LETSENCRYPT_EMAIL); needed with --domain
  --letsencrypt-staging  Certificates from Let's Encrypt's staging server
                         (not trusted by browsers; for tests)
  --no-traefik           Remove Traefik again; the console answers on its
                         own port as before
  --no-move              postgres: do not move the data; the install stays on
                         SQLite and nothing changes
  --overwrite-postgres   postgres: delete the PostgreSQL volume of an earlier
                         try before the move
  --user NAME            reset-password: the account (admin)
  --purge                uninstall: also delete the volumes (all data)
  --yes                  Answer yes to every question
  --adopt                Take over a NeoDesk container of another install
                         (an older manager, a split install, another folder):
                         its data is copied into this install's volumes and
                         the old container is kept, stopped and renamed
  --no-pull              Use the image that is on this machine (air-gapped,
                         docker load)
  --build                Build the image from this checkout instead of pulling
  --no-install-docker    Stop with an error when Docker is missing instead of
                         installing it
  --help                 This text

  ADMIN_PASSWORD=...     The admin's password at the first start; without it
                         the container makes a random one, printed once at
                         the end of the install

Repair and checks:
  --rescue               Repair volume permissions, start the container, check
  --diagnose             Read-only diagnostics
  --repair-permissions   Repair the volumes' owner (PUID/PGID, 10001)
  --check-permissions    Check that Docker, Compose and the folder are usable
  --set-config KEY=VALUE An allowed server setting into .env, then restart
                         (repeatable)

Files: <folder>/docker-compose.yml (replaced on update; put your own changes
into docker-compose.override.yml next to it), <folder>/.env (the settings,
every KEY=value reaches the server), <folder>/backups.
HTTPS for the console: --domain and --email (Traefik with Let's Encrypt), a
reverse proxy of your own with TLS in front (TRUST_PROXY=1 in .env), or
HTTPS_ENABLED=true with SSL_CERT_PATH and SSL_KEY_PATH naming
files in the data volume (/opt/neodesk/data/...); the console then answers on
port 5443.
Logs: cd <folder> && docker compose logs -f neodesk
EOF
}

parse_args() {
    while [ $# -gt 0 ]; do
        case "$1" in
            install|update|status|logs|backup|reset-password|postgres|uninstall|menu)
                [ -z "$COMMAND" ] || die "Two commands given: $COMMAND and $1" "Run one at a time."
                COMMAND="$1" ;;
            --dir) [ $# -ge 2 ] || die "--dir needs a folder"; NEODESK_DIR="$2"; shift ;;
            --dir=*) NEODESK_DIR="${1#*=}" ;;
            --tag) [ $# -ge 2 ] || die "--tag needs a tag"; NEODESK_IMAGE_TAG_SET="$2"; shift ;;
            --tag=*) NEODESK_IMAGE_TAG_SET="${1#*=}" ;;
            --relay) [ $# -ge 2 ] || die "--relay needs an address"; RELAY_SERVERS_SET="$2"; shift ;;
            --relay=*) RELAY_SERVERS_SET="${1#*=}" ;;
            --relay-mode) [ $# -ge 2 ] || die "--relay-mode needs auto, public or local"; RELAY_MODE="$2"; shift ;;
            --relay-mode=*) RELAY_MODE="${1#*=}" ;;
            --console-port) [ $# -ge 2 ] || die "--console-port needs a port"; CONSOLE_PORT_SET="$2"; shift ;;
            --console-port=*) CONSOLE_PORT_SET="${1#*=}" ;;
            --user) [ $# -ge 2 ] || die "--user needs an account name"; RESET_USER="$2"; shift ;;
            --user=*) RESET_USER="${1#*=}" ;;
            --postgres) USE_POSTGRES=true ;;
            --domain) [ $# -ge 2 ] || die "--domain needs a DNS name"; DOMAIN_SET="$2"; shift ;;
            --domain=*) DOMAIN_SET="${1#*=}" ;;
            --email) [ $# -ge 2 ] || die "--email needs an e-mail address"; LETSENCRYPT_EMAIL_SET="$2"; shift ;;
            --email=*) LETSENCRYPT_EMAIL_SET="${1#*=}" ;;
            --letsencrypt-staging) LETSENCRYPT_STAGING=true ;;
            --no-traefik) TRAEFIK_ACTION=off ;;
            --no-move) MOVE_DATA=no ;;
            --overwrite-postgres) OVERWRITE_POSTGRES=true ;;
            --purge) PURGE=true ;;
            --yes|-y) ASSUME_YES=true ;;
            --adopt) ADOPT=true ;;
            --no-pull) NO_PULL=true ;;
            --build) BUILD_LOCAL=true ;;
            --no-install-docker|--skip-docker-install) INSTALL_DOCKER=false ;;
            --rescue) COMMAND="rescue" ;;
            --diagnose|--diagnostics) COMMAND="diagnose" ;;
            --repair-permissions) COMMAND="repair-permissions" ;;
            --check-permissions|--check-capabilities) COMMAND="check" ;;
            --set-config)
                if [ $# -lt 2 ] || [[ "$2" != *=* ]]; then die "--set-config needs KEY=VALUE"; fi
                MANAGED_CONFIG_CHANGES+=("$2"); COMMAND="${COMMAND:-set-config}"; shift ;;
            -h|--help) usage; exit 0 ;;
            *) die "Unknown option: $1" "See: bash neodesk-docker.sh --help" ;;
        esac
        shift
    done
    COMMAND="${COMMAND:-install}"
    case "$RELAY_MODE" in
        auto|public|wan|local|lan) ;;
        *) die "--relay-mode must be auto, public or local, not $RELAY_MODE" ;;
    esac
    if [ -n "$CONSOLE_PORT_SET" ] && ! [[ "$CONSOLE_PORT_SET" =~ ^[0-9]+$ && "$CONSOLE_PORT_SET" -ge 1 && "$CONSOLE_PORT_SET" -le 65535 ]]; then
        die "--console-port must be a port number, not $CONSOLE_PORT_SET"
    fi
    case "${USE_POSTGRES,,}" in
        1|y|yes|true|on) USE_POSTGRES=true ;;
        *) USE_POSTGRES=false ;;
    esac
    case "${LETSENCRYPT_STAGING,,}" in
        1|y|yes|true|on) LETSENCRYPT_STAGING=true ;;
        *) LETSENCRYPT_STAGING=false ;;
    esac
    DOMAIN_SET="${DOMAIN_SET,,}"
    if [ -n "$DOMAIN_SET" ]; then
        [ "$TRAEFIK_ACTION" != off ] || die "--domain and --no-traefik contradict each other" "Give one of them."
        valid_domain "$DOMAIN_SET" || die "--domain must be a DNS name such as console.example.com, not $DOMAIN_SET" \
            "Let's Encrypt gives no certificate for an IP address or a name without a dot."
        TRAEFIK_ACTION=on
    fi
    if [ -n "$LETSENCRYPT_EMAIL_SET" ] && ! valid_email "$LETSENCRYPT_EMAIL_SET"; then
        die "--email must be an e-mail address, not $LETSENCRYPT_EMAIL_SET"
    fi
    if [ "$TRAEFIK_ACTION" = on ] && [ -z "$LETSENCRYPT_EMAIL_SET" ] && [ -z "$(env_get LETSENCRYPT_EMAIL)" ]; then
        die "--domain needs --email: Let's Encrypt's contact for expiry notices." \
            "sudo bash neodesk-docker.sh --domain $DOMAIN_SET --email admin@example.com"
    fi
}

# valid_domain NAME: a DNS name with at least one dot, no IP address
valid_domain() {
    [[ "$1" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$ ]] && [ "${#1}" -le 253 ]
}

valid_email() { [[ "$1" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; }

# confirm QUESTION DEFAULT(y|n): yes or no; --yes says yes, and without a
# terminal the default is taken
confirm() {
    local answer hint="[y/N]"
    [ "$2" = y ] && hint="[Y/n]"
    if [ "$ASSUME_YES" = true ]; then return 0; fi
    if [ ! -t 0 ]; then [ "$2" = y ]; return; fi
    read -r -p "$1 $hint " answer || answer=""
    answer="${answer:-$2}"
    [[ "$answer" =~ ^[YyJj] ]]
}

#===============================================================================
# The install folder: docker-compose.yml and .env
#===============================================================================

compose_file() { printf '%s\n' "$NEODESK_DIR/docker-compose.yml"; }
env_file() { printf '%s\n' "$NEODESK_DIR/.env"; }

# compose ARGS...: docker compose in the install folder (its .env and
# docker-compose.override.yml included)
compose() {
    (cd "$NEODESK_DIR" && docker compose "$@")
}

# compose_quiet ARGS...: docker compose without its progress lines; they
# are shown when it fails
compose_quiet() {
    local out
    if ! out=$(compose "$@" 2>&1); then
        printf '%s\n' "$out" >&2
        return 1
    fi
}

# settings_fingerprint: a hash of what the container is made from: the
# compose files and .env
settings_fingerprint() {
    local f files=()
    for f in "$(compose_file)" "$NEODESK_DIR/docker-compose.override.yml" "$(env_file)"; do
        [ ! -f "$f" ] || files+=("$f")
    done
    cat "${files[@]}" | sha256sum | cut -d' ' -f1
}

# remember_applied: the settings the running container was made from
remember_applied() { settings_fingerprint > "$NEODESK_DIR/.applied.sha256"; }

# env_get KEY: the value of KEY in .env, empty when it has none
env_get() {
    local file
    file=$(env_file)
    [ -f "$file" ] || return 0
    grep -E "^${1}=" "$file" 2>/dev/null | tail -n 1 | cut -d= -f2- | tr -d '\r' || true
}

# env_set KEY VALUE: one line KEY=VALUE in .env, other lines as they are
env_set() {
    local file tmp
    file=$(env_file)
    tmp="${file}.tmp.$$"
    (
        umask 077
        if [ -f "$file" ]; then
            grep -vE "^${1}=" "$file" > "$tmp" || true
        else
            printf '# NeoDesk settings: one KEY=value per line; every key reaches the server.\n' > "$tmp"
        fi
        printf '%s=%s\n' "$1" "$2" >> "$tmp"
    )
    chmod 600 "$tmp"
    mv "$tmp" "$file"
}

env_unset() {
    local file tmp
    file=$(env_file)
    [ -f "$file" ] || return 0
    tmp="${file}.tmp.$$"
    (umask 077; grep -vE "^${1}=" "$file" > "$tmp" || true)
    chmod 600 "$tmp"
    mv "$tmp" "$file"
}

# profile_on NAME, profile_off NAME: an optional service of the compose
# file (postgres, guacd) on or off in COMPOSE_PROFILES, the others kept
profile_on() {
    local list
    list=$(env_get COMPOSE_PROFILES)
    case ",$list," in
        *",$1,"*) return 0 ;;
    esac
    env_set COMPOSE_PROFILES "${list:+$list,}$1"
}

profile_off() {
    local list
    list=$(env_get COMPOSE_PROFILES | tr ',' '\n' | grep -vx "$1" | paste -sd, - || true)
    if [ -n "$list" ]; then env_set COMPOSE_PROFILES "$list"; else env_unset COMPOSE_PROFILES; fi
}

image_tag() {
    local tag
    tag="${NEODESK_IMAGE_TAG_SET:-$(env_get NEODESK_IMAGE_TAG)}"
    printf '%s\n' "${tag:-latest}"
}

image_ref() { printf '%s:%s\n' "$NEODESK_IMAGE" "$(image_tag)"; }

console_port() {
    local port
    port="${CONSOLE_PORT_SET:-$(env_get NEODESK_CONSOLE_PORT)}"
    printf '%s\n' "${port:-5000}"
}

# The host port of the console's HTTPS, which the compose file publishes
# whether HTTPS is on or not
console_https_port() {
    local port
    port="$(env_get NEODESK_CONSOLE_HTTPS_PORT)"
    printf '%s\n' "${port:-5443}"
}

on_postgres() {
    case "$(env_get DATABASE_URL)" in
        postgres://*|postgresql://*) return 0 ;;
    esac
    return 1
}

# traefik_on: the install runs Traefik in front of the console: it is
# being turned on now, or .env has the profile and it is not being turned off
traefik_on() {
    case "$TRAEFIK_ACTION" in
        on) return 0 ;;
        off) return 1 ;;
    esac
    case ",$(env_get COMPOSE_PROFILES)," in
        *,traefik,*) return 0 ;;
    esac
    return 1
}

# console_domain: the console's DNS name under Traefik, empty without
console_domain() {
    traefik_on || return 0
    printf '%s\n' "${DOMAIN_SET:-$(env_get NEODESK_DOMAIN)}"
}

sha_of() { sha256sum "$1" 2>/dev/null | cut -d' ' -f1; }

# fetch_compose_file DEST: the release's compose file, from this checkout
# when the script sits in one, else from GitHub
fetch_compose_file() {
    local src="$SCRIPT_DIR/docker-compose.quick.single.yml"
    if [ -f "$src" ]; then
        cp "$src" "$1"
        return 0
    fi
    curl -fsSL --connect-timeout 15 --max-time 60 -o "$1" "$NEODESK_RAW_BASE/docker-compose.quick.single.yml" \
        || die "Could not download the compose file from $NEODESK_RAW_BASE" \
            "Check the internet connection, or run the script from a checkout of the repository."
    grep -q 'ghcr.io/neodeskapp/neodesk' "$1" || die "The downloaded compose file is not NeoDesk's ($NEODESK_RAW_BASE)"
}

# write_compose_file: docker-compose.yml of this release. A file the user
# changed since this script wrote it stays; the new one goes next to it.
write_compose_file() {
    local dest new record
    dest=$(compose_file)
    record="$NEODESK_DIR/.compose.sha256"
    new="$dest.new.$$"
    fetch_compose_file "$new"
    if [ -f "$dest" ]; then
        if [ -f "$record" ] && [ "$(sha_of "$dest")" != "$(cat "$record")" ]; then
            if cmp -s "$dest" "$new"; then
                rm -f "$new"
            else
                mv "$new" "$dest.new"
                warn "docker-compose.yml was changed by hand, so it stays. The release's file is $dest.new."
                warn "Keep your own changes in docker-compose.override.yml next to it; Docker Compose reads it as well."
            fi
            return 0
        fi
        if [ ! -f "$record" ] && ! cmp -s "$dest" "$new"; then
            cp -p "$dest" "$dest.bak-$(date +%Y%m%d-%H%M%S)"
            say "The previous docker-compose.yml is kept as $(basename "$dest").bak-*"
        fi
    fi
    mv "$new" "$dest"
    chmod 644 "$dest"
    sha_of "$dest" > "$record"
}

#===============================================================================
# Checks: sudo, Docker, Compose, ports, internet
#===============================================================================

need_root_or_docker() {
    if [ "$(id -u)" -eq 0 ]; then return 0; fi
    if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 \
        && mkdir -p "$NEODESK_DIR" 2>/dev/null && [ -w "$NEODESK_DIR" ]; then
        return 0
    fi
    die "This needs root: run it with sudo." \
        "sudo bash neodesk-docker.sh ${COMMAND}" \
        "Without sudo it works when your user may use Docker (the docker group) and write $NEODESK_DIR (--dir)."
}

install_docker_engine() {
    if [ "$(id -u)" -ne 0 ]; then
        die "Docker is not installed, and installing it needs root." "Run: sudo bash neodesk-docker.sh"
    fi
    if ! command -v curl >/dev/null 2>&1; then
        die "Docker is not installed, and curl is missing to install it." \
            "Install curl (apt-get install -y curl), then run this again."
    fi
    step "Installing Docker with Docker's install script (get.docker.com)"
    local script
    script=$(mktemp)
    curl -fsSL --connect-timeout 15 --max-time 120 https://get.docker.com -o "$script" \
        || die "Could not download Docker's install script from get.docker.com." \
            "Check the internet connection, or install Docker yourself: https://docs.docker.com/engine/install/"
    sh "$script" >/dev/null || die "Docker's install script failed (see its output above)." \
        "Install Docker yourself: https://docs.docker.com/engine/install/"
    rm -f "$script"
    systemctl enable --now docker >/dev/null 2>&1 || true
    ok "Docker installed: $(docker --version 2>/dev/null)"
}

check_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        if [ "$INSTALL_DOCKER" = true ]; then
            install_docker_engine
        else
            die "Docker is not installed." \
                "Install it: curl -fsSL https://get.docker.com | sudo sh" \
                "or run this script without --no-install-docker, which does that."
        fi
    fi
    if ! docker info >/dev/null 2>&1; then
        if [ "$(id -u)" -eq 0 ] && command -v systemctl >/dev/null 2>&1; then
            systemctl start docker >/dev/null 2>&1 || true
        fi
        if ! docker info >/dev/null 2>&1; then
            if docker info 2>&1 | grep -qi 'permission denied'; then
                die "Your user may not use Docker." "Run with sudo, or add your user to the docker group and sign in again."
            fi
            die "Docker is installed but its daemon is not running." \
                "Start it: sudo systemctl start docker" "Its log: sudo journalctl -u docker --no-pager | tail -n 50"
        fi
    fi
    if ! docker compose version >/dev/null 2>&1; then
        if command -v docker-compose >/dev/null 2>&1; then
            die "Only the old docker-compose (v1) is installed; this needs Docker Compose v2 (docker compose)." \
                "Ubuntu: sudo apt-get install -y docker-compose-v2" \
                "Docker's repository (Debian, Ubuntu): sudo apt-get install -y docker-compose-plugin" \
                "Or install Docker with its compose plugin: curl -fsSL https://get.docker.com | sudo sh"
        fi
        die "Docker Compose v2 (docker compose) is missing." \
            "Ubuntu: sudo apt-get install -y docker-compose-v2" \
            "Docker's repository (Debian, Ubuntu): sudo apt-get install -y docker-compose-plugin"
    fi
    local version
    version=$(docker compose version --short 2>/dev/null | sed 's/^v//')
    if ! version_at_least "$version" 2.24.0; then
        die "Docker Compose $version is too old: 2.24 or newer is needed (it reads .env through env_file)." \
            "Update it: sudo apt-get install --only-upgrade docker-compose-plugin (or docker-compose-v2)"
    fi
}

# version_at_least A B: A >= B for dotted numbers
version_at_least() {
    [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n 1)" = "$2" ]
}

# port_user PROTO PORT: what listens on the port, empty when it is free
port_user() {
    local flag=-Htln line name
    [ "$1" = udp ] && flag=-Huln
    command -v ss >/dev/null 2>&1 || return 0
    line=$(ss "${flag}p" "sport = :$2" 2>/dev/null | head -n 1 || true)
    [ -n "$line" ] || return 0
    name=$(printf '%s' "$line" | grep -oE 'users:\(\("[^"]+"' | head -n 1 | cut -d'"' -f2 || true)
    printf '%s\n' "${name:-another program}"
}

check_ports() {
    local port user taken=() tcp=("$(console_port)" "$(console_https_port)" "${DOCKER_PORTS_TCP[@]}")
    ! traefik_on || tcp+=(80 443)
    for port in "${tcp[@]}"; do
        user=$(port_user tcp "$port")
        [ -z "$user" ] || taken+=("TCP $port: $user")
    done
    for port in "${DOCKER_PORTS_UDP[@]}"; do
        user=$(port_user udp "$port")
        [ -z "$user" ] || taken+=("UDP $port: $user")
    done
    [ "${#taken[@]}" -eq 0 ] && return 0
    local hint="Stop what uses them, then run this again."
    if printf '%s\n' "${taken[@]}" | grep -qE ': (hbbs|hbbr|rustdesk)'; then
        hint="A RustDesk server (hbbs, hbbr) uses them: stop it first, then run this again."
    fi
    if [ "${#taken[@]}" -eq 1 ] && [[ "${taken[0]}" == "TCP $(console_port):"* ]]; then
        hint="Stop it, or give the console another port: --console-port 5080"
    fi
    if [ "${#taken[@]}" -eq 1 ] && [[ "${taken[0]}" == "TCP $(console_https_port):"* ]]; then
        hint="Stop it, or give the console's HTTPS another port: NEODESK_CONSOLE_HTTPS_PORT=5444 in $(env_file)"
    fi
    if printf '%s\n' "${taken[@]}" | grep -qE '^TCP (80|443):'; then
        hint="Traefik needs ports 80 and 443 for the certificate and HTTPS: stop the web server that has them (or run without --domain and put NeoDesk behind it)."
    fi
    die "Ports NeoDesk needs are in use:" "${taken[@]}" "$hint"
}

# check_traefik_ports: 80 and 443 are free for a Traefik that does not run
# yet (an install that turns it on)
check_traefik_ports() {
    traefik_on || return 0
    [ "$(docker container inspect -f '{{.State.Running}}' "$TRAEFIK_CONTAINER" 2>/dev/null || true)" != true ] || return 0
    local port user taken=()
    for port in 80 443; do
        user=$(port_user tcp "$port")
        [ -z "$user" ] || taken+=("TCP $port: $user")
    done
    [ "${#taken[@]}" -eq 0 ] && return 0
    die "Traefik needs ports 80 and 443, but they are in use; nothing was changed:" "${taken[@]}" \
        "Stop the web server that has them, or run without --domain and put NeoDesk behind it (TRUST_PROXY=1)."
}

check_internet() {
    if [ "$NO_PULL" = true ] || [ "$BUILD_LOCAL" = true ]; then return 0; fi
    if ! curl -sS --connect-timeout 10 --max-time 20 -o /dev/null https://ghcr.io/v2/ 2>/dev/null; then
        die "This machine cannot reach ghcr.io, where the image is published." \
            "Check the internet connection and DNS (curl -I https://ghcr.io)." \
            "Behind a proxy, Docker needs it in its own settings: https://docs.docker.com/engine/daemon/proxy/" \
            "Without internet: docker load the image, then run this with --no-pull."
    fi
}

#===============================================================================
# Containers: which install is this, images, health
#===============================================================================

container_exists() { docker container inspect "$1" >/dev/null 2>&1; }

# container_dir NAME: the compose folder the container was made from
container_dir() {
    docker container inspect -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' "$1" 2>/dev/null || true
}

real_dir() { (cd "$1" 2>/dev/null && pwd -P) || printf '%s\n' "$1"; }

# install_state: "new", "ours" (this folder's install, its container made)
# or "foreign" (a NeoDesk container of another install). A folder whose
# container was never made (a first install that stopped early) is new.
install_state() {
    local c dir own
    own=$(real_dir "$NEODESK_DIR")
    for c in "${OLD_CONTAINERS[@]}"; do
        container_exists "$c" || continue
        dir=$(container_dir "$c")
        if [ "$c" = "$CONTAINER" ] && [ -n "$dir" ] && [ "$(real_dir "$dir")" = "$own" ]; then
            continue
        fi
        printf 'foreign\n'
        return 0
    done
    if container_exists "$CONTAINER"; then
        printf 'ours\n'
    else
        printf 'new\n'
    fi
}

foreign_install_error() {
    local c dir lines=()
    for c in "${OLD_CONTAINERS[@]}"; do
        container_exists "$c" || continue
        dir=$(container_dir "$c")
        lines+=("container $c (${dir:-not made by docker compose})")
    done
    die "Found NeoDesk from another install, so nothing was changed:" "${lines[@]}" \
        "Take it over into $NEODESK_DIR (its data is copied, the old container is kept stopped):" \
        "  sudo bash neodesk-docker.sh --adopt" \
        "Or manage it in its own folder: sudo bash neodesk-docker.sh --dir <that folder>"
}

pull_image() {
    if [ "$BUILD_LOCAL" = true ]; then
        build_image
        return 0
    fi
    if [ "$NO_PULL" = true ]; then
        docker image inspect "$(image_ref)" >/dev/null 2>&1 \
            || die "--no-pull, but the image $(image_ref) is not on this machine." "Load it first: docker load -i <file>"
        return 0
    fi
    step "Pulling $(image_ref)"
    local out ok=true
    out=$(compose pull --quiet neodesk 2>&1) || ok=false
    # Before the first release there is no latest image: without a tag given
    # or saved, the install runs the development image, as the native
    # installers take the development build then
    if [ "$ok" = false ] && printf '%s' "$out" | grep -qiE 'manifest unknown|not found' && [ "$(image_tag)" = latest ] \
        && [ -z "$NEODESK_IMAGE_TAG_SET" ] && [ -z "$(env_get NEODESK_IMAGE_TAG)" ]; then
        warn "ghcr.io has no release image yet (latest): running the development image dev instead."
        warn "Once a release is out: sudo bash neodesk-docker.sh update --tag latest"
        env_set NEODESK_IMAGE_TAG dev
        step "Pulling $(image_ref)"
        ok=true
        out=$(compose pull --quiet neodesk 2>&1) || ok=false
    fi
    if [ "$ok" = false ]; then
        if printf '%s' "$out" | grep -qiE 'manifest unknown|not found|denied|unauthorized'; then
            die "The image $(image_ref) was not found on ghcr.io." \
                "Check the tag (--tag, NEODESK_IMAGE_TAG in $(env_file)); latest is the newest release." \
                "Or build it from a checkout of the repository: sudo bash neodesk-docker.sh --build"
        fi
        printf '%s\n' "$out" >&2
        die "Pulling the image failed (output above)." "Check the internet connection and run this again."
    fi
    if on_postgres; then
        compose pull --quiet postgres >/dev/null 2>&1 || true
    fi
    if traefik_on; then
        compose pull --quiet traefik >/dev/null 2>&1 || true
    fi
}

# The data volume's place in images up to 3.6.0-rc.4. Images since
# 3.6.0-rc.5 have nothing there.
OLD_DATA_PATH=/opt/rustdesk

# set_data_path: where the compose file mounts the data volume for the
# image to run. An image of this release keeps its data in
# /opt/neodesk/data, the compose file's default, and the install's files
# that still name the old place move with it (move_old_data_path). One from
# before keeps it in /opt/rustdesk (its RUSTDESK_PATH), and .env says so
# with NEODESK_DATA_PATH.
set_data_path() {
    local path
    path=$(docker image inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$(image_ref)" 2>/dev/null \
        | sed -n 's/^RUSTDESK_PATH=//p' | head -n 1)
    if [ "$path" = "$OLD_DATA_PATH" ]; then
        env_set NEODESK_DATA_PATH "$OLD_DATA_PATH"
    else
        env_unset NEODESK_DATA_PATH
        move_old_data_path
    fi
}

# move_old_data_path: .env, a compose file changed by hand and
# docker-compose.override.yml get /opt/neodesk/data where they name the
# old place of the data volume (the compose files the data path of the
# release's compose file, so NEODESK_DATA_PATH counts there as well).
# Comments stay as they are. An update has the files as they were in its
# backup (keep_settings).
move_old_data_path() {
    local f new moved=()
    local old="$OLD_DATA_PATH([/\"':[:space:]]|\$)"
    for f in "$(env_file)" "$(compose_file)" "$NEODESK_DIR/docker-compose.override.yml"; do
        [ -f "$f" ] && grep -qE "^[^#]*$old" "$f" || continue
        if [ "$f" = "$(env_file)" ]; then
            new=/opt/neodesk/data
        else
            new='${NEODESK_DATA_PATH:-/opt/neodesk/data}'
        fi
        sed -i -E "/^[[:space:]]*#/!s#$old#$new\\1#g" "$f" || die "Could not rewrite $f"
        moved+=("$(basename "$f")")
    done
    [ "${#moved[@]}" -eq 0 ] \
        || say "Changed $OLD_DATA_PATH to /opt/neodesk/data, the data volume's place since 3.6.0-rc.5, in: ${moved[*]}"
}

build_image() {
    [ -f "$SCRIPT_DIR/Dockerfile" ] && [ -d "$SCRIPT_DIR/neodesk-server" ] \
        || die "--build needs a checkout of the repository next to this script." \
            "git clone https://github.com/neodeskapp/neodesk.git && sudo bash neodesk/neodesk-docker.sh --build"
    NEODESK_IMAGE_TAG_SET="local"
    env_set NEODESK_IMAGE_TAG local
    step "Building $(image_ref) from $SCRIPT_DIR (several minutes)"
    local commit
    commit=$(git -C "$SCRIPT_DIR" rev-parse HEAD 2>/dev/null || true)
    docker build -t "$(image_ref)" --build-arg NEODESK_COMMIT_SHA="$commit" "$SCRIPT_DIR" \
        || die "The image did not build (output above); the running container was not changed."
}

image_id() { docker image inspect -f '{{.Id}}' "$1" 2>/dev/null || true; }

container_image_id() { docker container inspect -f '{{.Image}}' "$CONTAINER" 2>/dev/null || true; }

container_running() {
    [ "$(docker container inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null || true)" = true ]
}

# answers URL: the URL answers, over HTTP or HTTPS
answers() {
    curl -fsk --max-time 3 -o /dev/null "$1" 2>/dev/null \
        || curl -fsk --max-time 3 -o /dev/null "${1/http:/https:}" 2>/dev/null
}

# wait_healthy: the API and the console answer on this machine within
# HEALTH_TIMEOUT seconds, while the container keeps running
wait_healthy() {
    local waited=0 port
    port=$(console_port)
    while [ "$waited" -lt "$HEALTH_TIMEOUT" ]; do
        if container_running \
            && answers "http://127.0.0.1:21121/api/health" \
            && answers "http://127.0.0.1:${port}/health"; then
            return 0
        fi
        sleep 2
        waited=$((waited + 2))
    done
    return 1
}

save_failed_log() {
    local file
    mkdir -p "$NEODESK_DIR/logs"
    file="$NEODESK_DIR/logs/failed-start-$(date +%Y%m%d-%H%M%S).log"
    docker logs --tail 300 "$CONTAINER" > "$file" 2>&1 || true
    printf '%s\n' "$file"
}

server_version() {
    docker exec "$CONTAINER" /usr/local/bin/neodesk-server -version 2>/dev/null | head -n 1 || printf 'version unknown\n'
}

#===============================================================================
# Backups of the volumes
#===============================================================================

backup_dir() { printf '%s\n' "$NEODESK_DIR/backups"; }

# backup_volumes [FOLDER]: a tar.gz of the data volumes, the folders
# neodesk (the data volume) and console (and a dump of PostgreSQL), taken
# while the container is stopped, with .env, docker-compose.yml and
# docker-compose.override.yml from FOLDER (an update's copies from before
# it) or the install folder. Prints the file.
backup_volumes() {
    local dir name image old from="${1:-$NEODESK_DIR}"
    dir=$(backup_dir)
    mkdir -p "$dir"
    chmod 700 "$dir"
    name="neodesk-$(date +%Y%m%d-%H%M%S)"
    image=$(docker container inspect -f '{{.Config.Image}}' "$CONTAINER" 2>/dev/null || true)
    if [ -z "$image" ] || ! docker image inspect "$image" >/dev/null 2>&1; then image=$(image_ref); fi
    docker run --rm --entrypoint tar --user 0 \
        -v "$DATA_VOLUME:/backup/neodesk:ro" -v "$CONSOLE_VOLUME:/backup/console:ro" -v "$dir:/out" \
        "$image" czf "/out/$name.tar.gz" -C /backup neodesk console >/dev/null \
        || die "The backup of the volumes failed; nothing was changed." "Is there free disk space? df -h $dir"
    cp -p "$from/docker-compose.yml" "$dir/$name.docker-compose.yml" 2>/dev/null || true
    cp -p "$from/docker-compose.override.yml" "$dir/$name.docker-compose.override.yml" 2>/dev/null || true
    cp -p "$from/.env" "$dir/$name.env" 2>/dev/null || true
    if on_postgres && container_exists "$PG_CONTAINER"; then
        compose exec -T postgres pg_dump -U neodesk neodesk 2>/dev/null | gzip > "$dir/$name.postgres.sql.gz" \
            || warn "The PostgreSQL dump failed; the volume backup is there."
    fi
    chmod 600 "$dir/$name".* 2>/dev/null || true
    # The last KEEP_BACKUPS stay
    ls -1t "$dir"/neodesk-*.tar.gz 2>/dev/null | tail -n +$((KEEP_BACKUPS + 1)) | while read -r old; do
        rm -f "$old" "${old%.tar.gz}".*
    done
    printf '%s\n' "$dir/$name.tar.gz"
}

# keep_settings: copies of .env, docker-compose.yml, its record and
# docker-compose.override.yml as they are before an update writes new ones,
# in a folder of the install folder that only its owner reads. The update's
# backup takes these, and a failed update puts them back. Prints the folder.
keep_settings() {
    local dir="$NEODESK_DIR/.before-update" f
    rm -rf "$dir"
    (umask 077 && mkdir -p "$dir")
    for f in "$(env_file)" "$(compose_file)" "$NEODESK_DIR/.compose.sha256" "$NEODESK_DIR/docker-compose.override.yml"; do
        [ ! -f "$f" ] || cp -p "$f" "$dir/"
    done
    printf '%s\n' "$dir"
}

# put_settings_back FOLDER: .env, docker-compose.yml, its record and
# docker-compose.override.yml as keep_settings kept them in FOLDER; one
# that was not there goes
put_settings_back() {
    local f
    for f in .env docker-compose.yml .compose.sha256 docker-compose.override.yml; do
        if [ -f "$1/$f" ]; then
            cp -p "$1/$f" "$NEODESK_DIR/$f"
        else
            rm -f "${NEODESK_DIR:?}/$f"
        fi
    done
}

# restore_volumes FILE: the volumes as the backup has them. The data volume
# is the archive's folder neodesk, or rustdesk in a backup of 3.6.0-rc.4 or
# older: it is mounted under both names.
restore_volumes() {
    docker run --rm --entrypoint sh --user 0 \
        -v "$DATA_VOLUME:/backup/neodesk" -v "$CONSOLE_VOLUME:/backup/console" \
        -v "$DATA_VOLUME:/backup/rustdesk" -v "$(dirname "$1"):/in:ro" \
        "$(image_ref)" -c 'find /backup/neodesk /backup/console -mindepth 1 -delete && tar xzf "/in/$1" -C /backup' \
        sh "$(basename "$1")"
}

#===============================================================================
# Install and update
#===============================================================================

detect_relay_address() {
    local ip=""
    case "$RELAY_MODE" in
        local|lan) ;;
        *)
            ip=$(curl -4 -fsS --max-time 5 https://checkip.amazonaws.com 2>/dev/null \
                || curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)
            ip=$(printf '%s' "$ip" | tr -d '[:space:]')
            ;;
    esac
    if [ -z "$ip" ]; then
        ip=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -n 1 || true)
        [ -n "$ip" ] || ip=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
        case "$RELAY_MODE" in
            local|lan) ;;
            *) warn "No public address found; clients get the LAN address $ip. Set another with --relay ADDRESS." ;;
        esac
    fi
    printf '%s\n' "$ip"
}

write_settings() {
    mkdir -p "$NEODESK_DIR"
    chmod 755 "$NEODESK_DIR"
    [ -f "$(env_file)" ] || env_set NEODESK_DOCKER_LAYOUT single
    [ -z "$NEODESK_IMAGE_TAG_SET" ] || env_set NEODESK_IMAGE_TAG "$NEODESK_IMAGE_TAG_SET"
    [ -z "$CONSOLE_PORT_SET" ] || env_set NEODESK_CONSOLE_PORT "$CONSOLE_PORT_SET"
    [ -z "${PUID:-}" ] || env_set PUID "$PUID"
    [ -z "${PGID:-}" ] || env_set PGID "$PGID"
    if [ -n "$RELAY_SERVERS_SET" ]; then
        env_set RELAY_SERVERS "$RELAY_SERVERS_SET"
    elif [ -z "$(env_get RELAY_SERVERS)" ]; then
        env_set RELAY_SERVERS "$(detect_relay_address)"
    fi
    case "$TRAEFIK_ACTION" in
        on) traefik_settings ;;
        off) no_traefik_settings ;;
    esac
}

# traefik_settings: Traefik with Let's Encrypt in front of the console:
# the profile, the domain and the contact, the console's own port on this
# host only, the client's address from Traefik and the address in mails
traefik_settings() {
    local email old_domain
    email="${LETSENCRYPT_EMAIL_SET:-$(env_get LETSENCRYPT_EMAIL)}"
    [ -n "$email" ] || die "--domain needs --email: Let's Encrypt's contact for expiry notices." \
        "sudo bash neodesk-docker.sh --domain $DOMAIN_SET --email admin@example.com"
    old_domain=$(env_get NEODESK_DOMAIN)
    check_domain_dns "$DOMAIN_SET"
    env_set NEODESK_DOMAIN "$DOMAIN_SET"
    env_set LETSENCRYPT_EMAIL "$email"
    if [ "$LETSENCRYPT_STAGING" = true ]; then
        env_set LETSENCRYPT_CA_SERVER "$LETSENCRYPT_STAGING_CA"
    else
        env_unset LETSENCRYPT_CA_SERVER
    fi
    env_set NEODESK_CONSOLE_BIND 127.0.0.1
    env_set TRUST_PROXY 1
    # A mail address of the console set by hand stays, one this script set
    # for an older domain follows the new one
    case "$(env_get PANEL_PUBLIC_URL)" in
        ""|"https://$old_domain") env_set PANEL_PUBLIC_URL "https://$DOMAIN_SET" ;;
    esac
    profile_on traefik
}

# no_traefik_settings: the settings traefik_settings made go again, and
# the Traefik container with them; its certificate volume stays
no_traefik_settings() {
    local domain
    domain=$(env_get NEODESK_DOMAIN)
    case ",$(env_get COMPOSE_PROFILES)," in
        *,traefik,*) ;;
        *) [ -n "$domain" ] || return 0 ;;
    esac
    step "Removing Traefik; the console answers on port $(console_port) again"
    compose --profile traefik rm -sf traefik >/dev/null 2>&1 || true
    profile_off traefik
    [ "$(env_get PANEL_PUBLIC_URL)" != "https://$domain" ] || env_unset PANEL_PUBLIC_URL
    [ "$(env_get NEODESK_CONSOLE_BIND)" != 127.0.0.1 ] || env_unset NEODESK_CONSOLE_BIND
    env_unset TRUST_PROXY
    env_unset NEODESK_DOMAIN
    env_unset LETSENCRYPT_CA_SERVER
}

# check_domain_dns NAME: warns when NAME does not point at this server's
# public address; Let's Encrypt checks the name over it
check_domain_dns() {
    local addrs relay
    command -v getent >/dev/null 2>&1 || return 0
    addrs=$(getent ahosts "$1" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ' || true)
    if [ -z "$addrs" ]; then
        warn "$1 does not resolve yet. Let's Encrypt gives the certificate once it points at this server;"
        warn "Traefik keeps trying. Its log: cd $NEODESK_DIR && docker compose logs -f traefik"
        return 0
    fi
    relay="${RELAY_SERVERS_SET:-$(env_get RELAY_SERVERS)}"
    relay="${relay%%:*}"
    if [[ "$relay" =~ ^[0-9.]+$ ]] && [[ " $addrs" != *" $relay "* ]]; then
        warn "$1 points at ${addrs% }, not at this server's address $relay."
        warn "Let's Encrypt reaches the name over ports 80 and 443; check the DNS record (or ignore this behind NAT)."
    fi
}

# wait_certificate: the console answers over HTTPS at its domain with a
# certificate this machine trusts (any certificate with the staging
# server) within CERT_TIMEOUT seconds; a warning only when not, as
# Traefik keeps trying
wait_certificate() {
    local domain waited=0 insecure=()
    domain=$(console_domain)
    [ -n "$domain" ] || return 0
    [ "$(env_get LETSENCRYPT_CA_SERVER)" != "$LETSENCRYPT_STAGING_CA" ] || insecure=(-k)
    step "Waiting for the Let's Encrypt certificate of $domain"
    while [ "$waited" -lt "$CERT_TIMEOUT" ]; do
        if curl -fsS "${insecure[@]}" --max-time 5 -o /dev/null \
            --resolve "$domain:443:127.0.0.1" "https://$domain/health" 2>/dev/null; then
            ok "HTTPS: https://$domain has its certificate (Traefik renews it by itself)"
            return 0
        fi
        sleep 3
        waited=$((waited + 3))
    done
    warn "https://$domain has no valid certificate yet. Let's Encrypt needs $domain to point at this server"
    warn "and ports 80 and 443 to reach it; Traefik keeps trying. Its log: cd $NEODESK_DIR && docker compose logs traefik"
}

new_secret() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex "$1"
    else
        head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'
    fi
}

# postgres_url PASSWORD: the server's address of the postgres service
postgres_url() { printf 'postgres://neodesk:%s@postgres:5432/neodesk?sslmode=disable\n' "$1"; }

# use_database ADDRESS: the server's database in .env, DATABASE_URL alone
# (the type follows from the address); the DB_TYPE of an older .env goes
use_database() {
    env_unset DB_TYPE
    env_set DATABASE_URL "$1"
}

# postgres_settings: the settings of the postgres service with a new
# password (kept when .env has one)
postgres_settings() {
    local pass
    pass=$(env_get POSTGRES_PASSWORD)
    [ -n "$pass" ] || pass=$(new_secret 24)
    env_set POSTGRES_PASSWORD "$pass"
    profile_on postgres
    use_database "$(postgres_url "$pass")"
}

do_install() {
    need_root_or_docker
    check_docker
    local state
    state=$(install_state)
    if [ "$state" = foreign ] && [ "$ADOPT" != true ]; then
        foreign_install_error
    fi
    if [ "$state" = ours ]; then
        [ "$COMMAND" != install ] || say "NeoDesk is installed in $NEODESK_DIR: updating it."
        do_update
        return
    fi
    if [ "$COMMAND" = update ]; then
        die "There is no install in $NEODESK_DIR to update." \
            "Install it: sudo bash neodesk-docker.sh" "Another folder: --dir <folder>"
    fi

    local started=$SECONDS
    say "${BOLD}Installing NeoDesk on Docker into $NEODESK_DIR${NC}"
    [ "$state" = foreign ] || check_ports
    check_internet
    if [ "$state" != foreign ] && docker volume inspect "$DATA_VOLUME" >/dev/null 2>&1; then
        say "The volume $DATA_VOLUME is there from an earlier install; its data is used."
    fi
    write_settings
    if [ "$USE_POSTGRES" = true ] && ! on_postgres; then
        postgres_settings
    fi
    write_compose_file
    pull_image
    set_data_path
    if [ "$state" = foreign ]; then
        adopt_old_install
    fi
    step "Starting NeoDesk"
    export ADMIN_PASSWORD
    compose_quiet up -d --remove-orphans \
        || die "docker compose up failed (output above)." "Its log: cd $NEODESK_DIR && docker compose logs neodesk"
    if ! wait_healthy; then
        local log
        log=$(save_failed_log)
        die "NeoDesk did not get healthy within ${HEALTH_TIMEOUT}s." "Its log is in $log" \
            "Follow it: cd $NEODESK_DIR && docker compose logs -f neodesk"
    fi
    remember_applied
    ok "NeoDesk runs ($((SECONDS - started))s)"
    wait_certificate
    print_summary first
}

do_update() {
    need_root_or_docker
    check_docker
    case "$(install_state)" in
        ours) ;;
        foreign) foreign_install_error ;;
        *) die "There is no install in $NEODESK_DIR to update." "Install it: sudo bash neodesk-docker.sh" ;;
    esac
    check_internet
    check_traefik_ports
    local old_image old_ref new_image applied backup log before
    # The settings, the compose file and the image from before the update:
    # the backup takes these, and the way back puts them back
    before=$(keep_settings)
    old_image=$(container_image_id)
    old_ref=$(docker container inspect -f '{{.Config.Image}}' "$CONTAINER" 2>/dev/null || true)
    write_settings
    write_compose_file
    pull_image
    set_data_path
    new_image=$(image_id "$(image_ref)")
    applied=$(cat "$NEODESK_DIR/.applied.sha256" 2>/dev/null || true)
    if [ -n "$old_image" ] && [ "$old_image" = "$new_image" ] && [ "$applied" = "$(settings_fingerprint)" ] \
        && container_running && wait_healthy; then
        rm -rf "$before"
        ok "NeoDesk is up to date: $(server_version)"
        print_summary
        return 0
    fi

    step "Stopping NeoDesk for the backup"
    compose stop -t 30 neodesk >/dev/null 2>&1 || true
    backup=$(backup_volumes "$before")
    ok "Backup: $backup"
    step "Starting $(image_ref)"
    compose_quiet up -d --remove-orphans || true
    if wait_healthy; then
        rm -rf "$before"
        remember_applied
        ok "Updated: $(server_version)"
        [ "$TRAEFIK_ACTION" != on ] || wait_certificate
        print_summary
        return 0
    fi

    log=$(save_failed_log)
    warn "The new container did not get healthy within ${HEALTH_TIMEOUT}s; its log is in $log"
    # The way back: .env and docker-compose.yml from before the update, so
    # Compose names the previous image again; neither --tag nor
    # NEODESK_IMAGE_TAG of this run counts any more
    put_settings_back "$before"
    rm -rf "$before"
    NEODESK_IMAGE_TAG_SET=""
    unset NEODESK_IMAGE_TAG
    if [ -n "$old_image" ] && [ "$old_image" != "$new_image" ]; then
        step "Going back to the previous image"
        # Under its own name: the pull may have moved that tag (latest) to
        # the new image
        if [ -n "$old_ref" ] && [ "${old_ref#sha256:}" = "$old_ref" ]; then
            docker tag "$old_image" "$old_ref" || warn "Could not name the previous image $old_ref again"
        fi
        compose_quiet up -d --force-recreate neodesk || true
        if wait_healthy; then
            die "The update failed; NeoDesk runs the previous version again: $(server_version)" \
                "The failed start's log: $log" "The data before the update: $backup"
        fi
    fi
    step "Putting the data of the backup back"
    compose stop -t 10 neodesk >/dev/null 2>&1 || true
    restore_volumes "$backup" || true
    compose_quiet up -d --force-recreate neodesk || true
    if wait_healthy; then
        die "The update failed; NeoDesk runs again with the data from before the update." \
            "The failed start's log: $log"
    fi
    die "NeoDesk does not start, also not with the previous version." \
        "The failed start's log: $log" "The data before the update: $backup" \
        "Follow the log: cd $NEODESK_DIR && docker compose logs -f neodesk"
}

# first_password: the admin's generated first password from the volume,
# empty when there is none
first_password() {
    docker exec -u neodesk "$CONTAINER" sh -c 'cat "${RUSTDESK_PATH:-/opt/neodesk/data}/.admin_credentials" 2>/dev/null' 2>/dev/null \
        | sed -n 's/^Admin Password:[[:space:]]*//p' | head -n 1 || true
}

public_key() {
    docker exec "$CONTAINER" sh -c 'cat "${RUSTDESK_PATH:-/opt/neodesk/data}/id_ed25519.pub" 2>/dev/null' 2>/dev/null | tr -d '\r\n' || true
}

# print_summary [first]: where the console is, how to sign in (the first
# password only right after the first install), the client settings and
# where the logs are
print_summary() {
    local addr port key password tag domain
    domain=$(console_domain)
    addr=$(env_get RELAY_SERVERS)
    addr="${addr%%:*}"
    addr="${addr:-<this server>}"
    port=$(console_port)
    key=$(public_key)
    tag=$(image_tag)
    say ""
    say "${BOLD}NeoDesk on Docker${NC}: $(server_version)"
    if [ "$tag" = latest ]; then
        say "Image:           $(image_ref), the newest release (pin one: --tag <version>)"
    else
        say "Image:           $(image_ref) (pinned; the newest release: --tag latest)"
    fi
    say ""
    if [ -n "$domain" ]; then
        say "Web console:     https://${domain}   (Let's Encrypt, renewed by Traefik; on this machine: http://127.0.0.1:${port})"
    else
        say "Web console:     http://${addr}:${port}   (on this machine: http://127.0.0.1:${port})"
    fi
    if [ "${1:-}" = first ]; then
        if [ -n "$ADMIN_PASSWORD" ]; then
            say "Sign in:         user admin, the password you set in ADMIN_PASSWORD"
        else
            password=$(first_password)
            if [ -n "$password" ]; then
                say "Sign in:         user admin, password ${BOLD}${password}${NC}"
                say "                 (shown this once; change it after the first sign-in)"
            else
                say "Sign in:         user admin; the password: cd $NEODESK_DIR && docker compose exec neodesk neodesk-show-admin-credentials"
            fi
        fi
    else
        say "Sign in:         with your accounts; a new admin password: sudo bash neodesk-docker.sh reset-password"
    fi
    say ""
    say "RustDesk clients (Settings, Network, ID/Relay server):"
    say "  ID server:     ${addr}"
    say "  Relay server:  ${addr}"
    say "  API server:    http://${addr}:21121"
    say "  Key:           ${key:-not made yet; later: docker exec neodesk cat /opt/neodesk/data/id_ed25519.pub}"
    say ""
    if [ -n "$domain" ]; then
        say "Ports to open in a firewall in front of this machine: TCP 80, 443, 21115-21119, 21121, 21122 and UDP 21116"
    else
        say "Ports to open in a firewall in front of this machine: TCP ${port}, 21115-21119, 21121, 21122 and UDP 21116"
    fi
    say "Logs:            cd $NEODESK_DIR && docker compose logs -f neodesk"
    say "Update:          sudo bash neodesk-docker.sh   (or: cd $NEODESK_DIR && docker compose pull && docker compose up -d)"
    say "Settings:        $(env_file)   (then: cd $NEODESK_DIR && docker compose up -d)"
}

#===============================================================================
# Taking over an older install
#===============================================================================

volume_is_empty() {
    docker volume inspect "$1" >/dev/null 2>&1 || return 0
    [ -z "$(docker run --rm --entrypoint sh -v "$1:/v:ro" "$(image_ref)" -c 'ls -A /v' 2>/dev/null)" ]
}

# mount_source CONTAINER PATH: the volume name or host folder mounted at PATH
mount_source() {
    docker container inspect -f "{{range .Mounts}}{{if eq .Destination \"$2\"}}{{if .Name}}{{.Name}}{{else}}{{.Source}}{{end}}{{end}}{{end}}" "$1" 2>/dev/null || true
}

# copy_mount CONTAINER PATH VOLUME: what the container has at PATH into the
# volume, unless it is that volume
copy_mount() {
    local src
    src=$(mount_source "$1" "$2")
    [ -n "$src" ] || return 0
    [ "$src" = "$3" ] && return 0
    if ! volume_is_empty "$3"; then
        die "The volume $3 holds data already, so $2 of $1 was not copied." \
            "Remove the volume if it is not needed (docker volume rm $3) and run --adopt again." \
            "The old container is stopped; start it again: docker start $1"
    fi
    docker volume create "$3" >/dev/null
    docker run --rm --entrypoint sh --user 0 --volumes-from "$1:ro" -v "$3:/to" "$(image_ref)" \
        -c 'cp -a "$1/." /to/' sh "$2" \
        || die "Copying $2 of $1 into $3 failed; the old container is stopped but unchanged." "Start it again: docker start $1"
    say "  copied $2 of $1 ($src) into the volume $3"
}

adopt_old_install() {
    local c stamp relay
    stamp=$(date +%Y%m%d-%H%M%S)
    for c in "${OLD_CONTAINERS[@]}"; do
        container_exists "$c" || continue
        if docker container inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$c" 2>/dev/null | grep -qE '^DATABASE_URL=postgres'; then
            die "The install of container $c runs on PostgreSQL; taking it over is not supported." \
                "Update it in its own folder instead: --dir <its folder>"
        fi
    done
    step "Taking over the old install (its containers are kept, stopped and renamed)"
    for c in "${OLD_CONTAINERS[@]}"; do
        container_exists "$c" || continue
        if [ -z "$RELAY_SERVERS_SET" ]; then
            relay=$(docker container inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$c" 2>/dev/null | sed -n 's/^RELAY_SERVERS=//p' | head -n 1)
            [ -z "$relay" ] || env_set RELAY_SERVERS "$relay"
        fi
        docker stop -t 30 "$c" >/dev/null 2>&1 || true
        # The data at /opt/neodesk/data, or at /opt/rustdesk in a container
        # of an image of 3.6.0-rc.4 or older
        copy_mount "$c" /opt/neodesk/data "$DATA_VOLUME"
        copy_mount "$c" "$OLD_DATA_PATH" "$DATA_VOLUME"
        copy_mount "$c" /app/data "$CONSOLE_VOLUME"
        docker rename "$c" "$c-old-$stamp" >/dev/null
        docker update --restart=no "$c-old-$stamp" >/dev/null 2>&1 || true
        say "  kept the old container as $c-old-$stamp (remove it later: docker rm $c-old-$stamp)"
    done
}

#===============================================================================
# Other commands
#===============================================================================

require_install() {
    need_root_or_docker
    check_docker
    [ -f "$(compose_file)" ] || die "There is no install in $NEODESK_DIR." \
        "Install it: sudo bash neodesk-docker.sh" "Another folder: --dir <folder>"
}

do_status() {
    require_install
    compose ps
    say ""
    if container_running; then
        say "Health: $(docker container inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}no check{{end}}' "$CONTAINER")"
        print_summary
    else
        say "NeoDesk is not running. Start it: cd $NEODESK_DIR && docker compose up -d"
    fi
}

do_logs() {
    require_install
    compose logs -f --tail 200 neodesk
}

do_backup() {
    require_install
    local was_running=false file
    container_running && was_running=true
    if [ "$was_running" = true ]; then
        step "Stopping NeoDesk for the backup"
        compose stop -t 30 neodesk >/dev/null
    fi
    file=$(backup_volumes)
    [ "$was_running" = false ] || compose start neodesk >/dev/null
    ok "Backup: $file"
}

do_reset_password() {
    require_install
    container_running || die "NeoDesk is not running." "Start it: cd $NEODESK_DIR && docker compose up -d"
    local password
    password=$(new_secret 12)
    # reset-password also removes the first password in the volume, which
    # signs nobody in any more
    printf '%s\n' "$password" | docker exec -i -u neodesk "$CONTAINER" \
        /usr/local/bin/neodesk-server reset-password "$RESET_USER" >/dev/null \
        || die "The password of $RESET_USER was not changed (does the account exist?)."
    say "New password of ${RESET_USER}: ${BOLD}${password}${NC}"
    say "(shown this once; sign in and change it)"
}

do_uninstall() {
    require_install
    local answer file
    if [ "$PURGE" = true ]; then
        if [ "$ASSUME_YES" != true ]; then
            [ -t 0 ] || die "--purge deletes every device, account and the server key; add --yes to do it without a question."
            read -r -p "Delete the volumes with all data and the server key? Type delete: " answer
            [ "$answer" = delete ] || die "Nothing was deleted."
        fi
        step "Backing up the data before it is deleted"
        # NeoDesk stops; the postgres service runs on for its dump
        compose stop -t 30 neodesk >/dev/null 2>&1 || true
        file=$(backup_volumes)
        ok "Backup: $file"
        compose_quiet --profile postgres --profile guacd --profile traefik down -v --remove-orphans
        docker volume rm "$DATA_VOLUME" "$CONSOLE_VOLUME" "$PG_VOLUME" "$TRAEFIK_VOLUME" >/dev/null 2>&1 || true
        ok "NeoDesk's containers and volumes are removed."
    else
        compose_quiet --profile postgres --profile guacd --profile traefik down --remove-orphans
        ok "NeoDesk's containers are removed."
        say "Left on this machine:"
        local listed="" removable="" v
        for v in "$PG_VOLUME" "$TRAEFIK_VOLUME"; do
            docker volume inspect "$v" >/dev/null 2>&1 || continue
            listed+=", $v"
            removable+=" $v"
        done
        say "  volumes:  $DATA_VOLUME (database, server key), $CONSOLE_VOLUME$listed"
        say "            an install uses them again; delete them: docker volume rm $DATA_VOLUME $CONSOLE_VOLUME$removable"
    fi
    say "  image:    $(image_ref)   (delete: docker image rm $(image_ref))"
    say "  folder:   $NEODESK_DIR with .env and the backups   (delete: sudo rm -rf $NEODESK_DIR)"
}

do_set_config() {
    require_install
    step "Applying allowed settings to $(env_file)"
    # The image's own neodesk-server checks the keys against its allowlist
    docker run --rm --user "$(id -u):$(id -g)" --entrypoint /usr/local/bin/neodesk-server \
        -v "$NEODESK_DIR:/cfg" -e NEODESK_ENV_FILE=/cfg/.env \
        "$(image_ref)" config set "${MANAGED_CONFIG_CHANGES[@]}" || die "The settings were refused (output above)."
    compose_quiet up -d || die "docker compose up failed (output above)."
    wait_healthy || die "NeoDesk did not get healthy after the change."
    remember_applied "Its log: cd $NEODESK_DIR && docker compose logs neodesk"
    ok "Settings applied"
}

do_check() {
    local failures=0
    if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
        warn "Docker is not usable"; failures=$((failures + 1))
    fi
    docker compose version >/dev/null 2>&1 || { warn "Docker Compose v2 is missing"; failures=$((failures + 1)); }
    [ -f "$(compose_file)" ] || { warn "No compose file: $(compose_file)"; failures=$((failures + 1)); }
    if ! { mkdir -p "$NEODESK_DIR" 2>/dev/null && [ -w "$NEODESK_DIR" ]; }; then
        warn "$NEODESK_DIR is not writable"; failures=$((failures + 1))
    fi
    if [ -f "$(compose_file)" ] && ! compose config --quiet 2>/dev/null; then
        warn "The compose configuration is invalid"; failures=$((failures + 1))
    fi
    if [ "$failures" -eq 0 ]; then
        ok "Docker, Compose and $NEODESK_DIR are usable"
        return 0
    fi
    return 1
}

do_repair_permissions() {
    require_install
    local uid gid v
    uid="${PUID:-$(env_get PUID)}"
    uid="${uid:-10001}"
    gid="${PGID:-$(env_get PGID)}"
    gid="${gid:-10001}"
    for v in "$DATA_VOLUME" "$CONSOLE_VOLUME"; do
        docker volume inspect "$v" >/dev/null 2>&1 || continue
        docker run --rm --entrypoint sh --user 0 -v "$v:/v" "$(image_ref)" -c "chown -R $uid:$gid /v && chmod -R u+rwX /v" \
            && ok "Owner of $v: $uid:$gid"
    done
}

do_rescue() {
    do_repair_permissions
    compose_quiet up -d || true
    if wait_healthy; then ok "NeoDesk runs"; else warn "NeoDesk is not healthy; see the diagnostics below."; fi
    do_diagnose
}

do_diagnose() {
    local port path
    if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
        say "Docker is not usable on this machine."
        return 0
    fi
    say "Install folder: $NEODESK_DIR"
    if [ -f "$(compose_file)" ]; then compose ps 2>/dev/null || true; else say "No compose file there."; fi
    say ""
    say "Last log lines of $CONTAINER:"
    docker logs --tail 30 "$CONTAINER" 2>&1 || say "  (no container $CONTAINER)"
    say ""
    for port in "$(console_port)" 21121; do
        path=health
        [ "$port" = 21121 ] && path=api/health
        if answers "http://127.0.0.1:${port}/${path}"; then say "Port $port: answers"; else say "Port $port: no answer"; fi
    done
    docker volume ls --format '{{.Name}}' 2>/dev/null | grep -E '^neodesk' | sed 's/^/Volume: /' || true
}

do_menu() {
    local choice
    while true; do
        say ""
        say "NeoDesk on Docker ($NEODESK_DIR)"
        say "  1) Install or update       5) Reset a password"
        say "  2) Status                  6) Move to PostgreSQL"
        say "  3) Logs                    7) Diagnostics"
        say "  4) Backup                  8) Uninstall (keeps the data)"
        say "  0) Quit"
        read -r -p "Choice: " choice || return 0
        case "$choice" in
            1) (COMMAND=install; do_install) || true ;;
            2) (do_status) || true ;;
            3) (do_logs) || true ;;
            4) (do_backup) || true ;;
            5) (do_reset_password) || true ;;
            6) (COMMAND=postgres; do_postgres) || true ;;
            7) (do_diagnose) || true ;;
            8) (do_uninstall) || true ;;
            0|q|"") return 0 ;;
        esac
    done
}

#===============================================================================
# SQLite to PostgreSQL
#===============================================================================

# The server's migrate command (sqlite2pg moves every table of the server
# and the console); an image older than the command answers its -h with 2
MIGRATE_TOOL="/usr/local/bin/neodesk-server migrate"

# wait_postgres: the postgres service answers within a minute
wait_postgres() {
    local waited=0
    while [ "$waited" -lt 60 ]; do
        [ "$(docker container inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$PG_CONTAINER" 2>/dev/null || true)" = healthy ] && return 0
        sleep 2
        waited=$((waited + 2))
    done
    return 1
}

# back_to_sqlite MESSAGE [HINT...]: the install as it was, on SQLite, and
# the error
back_to_sqlite() {
    env_unset DATABASE_URL
    env_unset DB_TYPE
    profile_off postgres
    compose --profile postgres stop -t 10 postgres >/dev/null 2>&1 || true
    compose_quiet up -d neodesk || true
    wait_healthy || warn "NeoDesk on SQLite did not answer either; its log: cd $NEODESK_DIR && docker compose logs neodesk"
    die "$@"
}

# do_postgres: moves the install from SQLite to the postgres service with
# the server's own tool (sqlite2pg: every table of the server and the
# console, ids kept). Declining the move changes nothing; a failed move
# or a server that does not get healthy on PostgreSQL leaves it on SQLite.
do_postgres() {
    require_install
    if on_postgres; then
        ok "The install runs on PostgreSQL already."
        return 0
    fi
    container_exists "$CONTAINER" || die "NeoDesk is not installed in $NEODESK_DIR yet." \
        "Install it on PostgreSQL right away: sudo bash neodesk-docker.sh --postgres"
    local image uid gid pass out
    image=$(docker container inspect -f '{{.Config.Image}}' "$CONTAINER")
    docker run --rm --entrypoint sh "$image" -c "$MIGRATE_TOOL -h" >/dev/null 2>&1 \
        || die "The image $image has no move tool yet." "Update first: sudo bash neodesk-docker.sh, then run this again."

    say "This moves every device, account and setting from SQLite into a PostgreSQL"
    say "container (neodesk-postgres, volume $PG_VOLUME). NeoDesk stops for it;"
    say "the data volumes are backed up first, and the SQLite file stays as it is."
    if [ "$MOVE_DATA" = no ] || ! confirm "Move the data into PostgreSQL? Without the move the install stays on SQLite." y; then
        say "Nothing changed: the install stays on SQLite."
        return 0
    fi

    if docker volume inspect "$PG_VOLUME" >/dev/null 2>&1; then
        [ "$OVERWRITE_POSTGRES" = true ] || die "The volume $PG_VOLUME holds a PostgreSQL database from before; nothing was changed." \
            "Delete it and move again: sudo bash neodesk-docker.sh postgres --overwrite-postgres"
        compose --profile postgres rm -sf postgres >/dev/null 2>&1 || true
        docker volume rm "$PG_VOLUME" >/dev/null || die "The volume $PG_VOLUME could not be deleted (is a container using it?)."
        env_unset POSTGRES_PASSWORD
    fi
    pass=$(env_get POSTGRES_PASSWORD)
    [ -n "$pass" ] || pass=$(new_secret 24)
    env_set POSTGRES_PASSWORD "$pass"

    step "Stopping NeoDesk for the backup and the move"
    compose stop -t 30 neodesk >/dev/null 2>&1 || true
    local backup
    backup=$(backup_volumes)
    ok "Backup: $backup"

    step "Starting PostgreSQL"
    compose_quiet --profile postgres up -d postgres || back_to_sqlite "PostgreSQL did not start; the install stays on SQLite." \
        "Its log: cd $NEODESK_DIR && docker compose logs postgres"
    wait_postgres || back_to_sqlite "PostgreSQL did not get ready within a minute; the install stays on SQLite." \
        "Its log: cd $NEODESK_DIR && docker compose logs postgres"

    step "Moving the data (the server's sqlite2pg)"
    uid=$(env_get PUID)
    gid=$(env_get PGID)
    # The address reaches the tool through the container's environment,
    # not its arguments on this machine
    if ! out=$(DATABASE_URL="$(postgres_url "$pass")" compose run --rm --no-deps -T --user "${uid:-10001}:${gid:-10001}" \
        --entrypoint sh neodesk -c "exec $MIGRATE_TOOL -mode sqlite2pg -src \"\$DB_PATH\" -dst \"\$DATABASE_URL\"" 2>&1); then
        printf '%s\n' "$out" >&2
        back_to_sqlite "The move failed (output above); the install stays on SQLite and nothing of it changed." \
            "Run it again after the cause is fixed: sudo bash neodesk-docker.sh postgres --overwrite-postgres"
    fi
    printf '%s\n' "$out" | grep -E 'Migration complete|Backup created' || true

    step "Starting NeoDesk on PostgreSQL"
    profile_on postgres
    use_database "$(postgres_url "$pass")"
    compose_quiet up -d || true
    if ! wait_healthy; then
        local log
        log=$(save_failed_log)
        back_to_sqlite "NeoDesk did not get healthy on PostgreSQL; it runs on SQLite again with its data as it was." \
            "The failed start's log: $log"
    fi
    remember_applied
    ok "NeoDesk runs on PostgreSQL. The SQLite file stays in the volume $DATA_VOLUME; the backup is $backup."
    print_summary
}

main() {
    parse_args "$@"
    case "$COMMAND" in install|update|menu) print_header ;; esac
    case "$COMMAND" in
        install|update) do_install ;;
        status) do_status ;;
        logs) do_logs ;;
        backup) do_backup ;;
        reset-password) do_reset_password ;;
        postgres) do_postgres ;;
        uninstall) do_uninstall ;;
        menu) do_menu ;;
        rescue) do_rescue ;;
        diagnose) do_diagnose ;;
        repair-permissions) do_repair_permissions ;;
        check) do_check ;;
        set-config) do_set_config ;;
    esac
}

# Run (tests source the functions with NEODESK_DOCKER_SOURCED=1)
if [ "${NEODESK_DOCKER_SOURCED:-0}" != 1 ]; then
    main "$@"
fi
