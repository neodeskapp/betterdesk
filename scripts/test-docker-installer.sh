#!/usr/bin/env bash
# Checks of betterdesk-docker.sh without Docker: the manager's functions
# are loaded without its main, and git, docker and docker compose are
# stand-ins that work on files in a temporary folder.
#
#   bash scripts/test-docker-installer.sh
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

# load: the manager's functions (without its last lines, which run main)
# with its folders in a temporary one and the interactive parts stubbed.
load() {
    set --
    # shellcheck disable=SC1090
    source <(sed '/^# Run$/,$d' "$SCRIPT_ROOT/betterdesk-docker.sh")
    set +eu
    TEST_DIR=$(mktemp -d)
    SCRIPT_DIR="$TEST_DIR/install"
    DATA_DIR="$TEST_DIR/data"
    BACKUP_DIR="$TEST_DIR/backups"
    COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
    COMPOSE_CALLS="$TEST_DIR/compose-calls"
    LOG_FILE=/dev/null
    mkdir -p "$SCRIPT_DIR" "$DATA_DIR" "$BACKUP_DIR"
    : > "$COMPOSE_CALLS"
    print_header() { :; }
    press_enter() { :; }
    detect_installation() { INSTALL_STATUS="installed"; AIO_RUNNING=true; }
    check_management_capabilities() { return 0; }
    preserve_compose_database_config() { :; }
    create_compose_file() { printf 'new compose\n' > "$COMPOSE_FILE"; }
    sleep() { :; }
    curl() { return 0; }
}

# The install's files before an update
write_old_tree() {
    mkdir -p "$SCRIPT_DIR/betterdesk-server" "$SCRIPT_DIR/docker" "$SCRIPT_DIR/web-ui/node_modules"
    printf 'old\n' > "$SCRIPT_DIR/betterdesk-server/go.mod"
    printf 'old\n' > "$SCRIPT_DIR/betterdesk-server/only-in-old.go"
    printf 'old\n' > "$SCRIPT_DIR/Dockerfile"
    printf 'old\n' > "$SCRIPT_DIR/docker/entrypoint.sh"
    printf 'old\n' > "$SCRIPT_DIR/web-ui/package.json"
    printf 'kept\n' > "$SCRIPT_DIR/web-ui/node_modules/dep.js"
    printf '3.5.200\n' > "$SCRIPT_DIR/VERSION"
    printf 'old compose\n' > "$COMPOSE_FILE"
}

# git clone that writes the release's files into the last argument and
# records the branch it was asked for in $TEST_DIR/cloned-branches
git() {
    if [ "${1:-}" = "clone" ]; then
        local dest="${!#}" prev="" arg
        for arg in "$@"; do
            [ "$prev" = --branch ] && printf '%s\n' "$arg" >> "$TEST_DIR/cloned-branches"
            prev=$arg
        done
        mkdir -p "$dest/betterdesk-server" "$dest/docker" "$dest/web-ui"
        printf 'new\n' > "$dest/betterdesk-server/go.mod"
        printf 'new\n' > "$dest/Dockerfile"
        printf 'new\n' > "$dest/docker/entrypoint.sh"
        printf 'new\n' > "$dest/web-ui/package.json"
        printf '3.5.264\n' > "$dest/VERSION"
        return 0
    fi
    return 1
}

# docker compose: records each call; the build fails when BUILD_FAILS=1
fake_compose() {
    printf '%s\n' "$1" >> "$COMPOSE_CALLS"
    if [ "$1" = build ] && [ "${BUILD_FAILS:-0}" = 1 ]; then
        echo "failed to solve: the build broke" >&2
        return 1
    fi
    return 0
}

# UP12: an update whose image does not build puts the previous files back
# and leaves the running container alone; one that builds keeps the new
# files and drops the copy.
test_failed_build_restores_sources() (
    load
    write_old_tree
    COMPOSE_CMD=fake_compose
    MENU_CHOICE=1
    menu_choose() { MENU_CHOICE=1; }
    do_backup_silent() { :; }
    export BUILD_FAILS=1
    ( do_update ) >/dev/null 2>&1
    [ "$(cat "$SCRIPT_DIR/betterdesk-server/go.mod")" = old ] || fail "the server source was not put back after a failed build"
    [ -f "$SCRIPT_DIR/betterdesk-server/only-in-old.go" ] || fail "a file of the old server source is gone after a failed build"
    [ "$(cat "$SCRIPT_DIR/Dockerfile")" = old ] || fail "the Dockerfile was not put back after a failed build"
    [ "$(cat "$SCRIPT_DIR/docker/entrypoint.sh")" = old ] || fail "docker/entrypoint.sh was not put back after a failed build"
    [ "$(cat "$SCRIPT_DIR/web-ui/package.json")" = old ] || fail "the web app source was not put back after a failed build"
    [ -f "$SCRIPT_DIR/web-ui/node_modules/dep.js" ] || fail "web-ui/node_modules was lost"
    [ "$(cat "$SCRIPT_DIR/VERSION")" = 3.5.200 ] || fail "VERSION was not put back after a failed build"
    [ "$(cat "$COMPOSE_FILE")" = "old compose" ] || fail "the compose file was not put back after a failed build"
    if grep -qx down "$COMPOSE_CALLS"; then fail "the running containers were stopped although the build failed"; fi

    : > "$COMPOSE_CALLS"
    export BUILD_FAILS=0
    ( do_update ) >/dev/null 2>&1
    [ "$(cat "$SCRIPT_DIR/betterdesk-server/go.mod")" = new ] || fail "a good update did not keep the new server source"
    [ ! -e "$SCRIPT_DIR/betterdesk-server/only-in-old.go" ] || fail "a good update kept a file the release no longer has"
    [ "$(cat "$SCRIPT_DIR/Dockerfile")" = new ] || fail "a good update did not keep the new Dockerfile"
    [ -f "$SCRIPT_DIR/web-ui/node_modules/dep.js" ] || fail "web-ui/node_modules was lost on a good update"
    [ "$(tr '\n' ' ' < "$COMPOSE_CALLS")" = "build down up " ] || fail "a good update ran compose as: $(tr '\n' ' ' < "$COMPOSE_CALLS")"
    if compgen -G "$SCRIPT_DIR/*.pre-update" >/dev/null || [ -e "$SCRIPT_DIR/.pre-update" ]; then
        fail "a good update left its copy of the previous files"
    fi
    rm -rf "$TEST_DIR"
)

# UP13: the backup holds the data folder with its dotfiles (.api_key,
# .admin_credentials) and the web console's data volume (/app/data).
test_backup_keeps_dotfiles_and_console_data() (
    load
    printf 'key\n' > "$DATA_DIR/.api_key"
    printf 'credentials\n' > "$DATA_DIR/.admin_credentials"
    printf 'db\n' > "$DATA_DIR/db_v2.sqlite3"
    printf 'old compose\n' > "$COMPOSE_FILE"
    docker() {
        case "$1" in
            ps) printf '%s\n' betterdesk ;;
            cp)
                [ "$2" = "betterdesk:/app/data/." ] || return 1
                mkdir -p "$3"
                printf 'auth\n' > "$3/auth.db"
                ;;
            *) return 1 ;;
        esac
    }
    ( do_backup_silent ) >/dev/null 2>&1
    local archive root
    archive=$(find "$BACKUP_DIR" -maxdepth 1 -name '*.tar.gz' | head -n 1)
    [ -n "$archive" ] || fail "no backup archive"
    mkdir -p "$TEST_DIR/unpacked"
    tar -xzf "$archive" -C "$TEST_DIR/unpacked"
    root=$(find "$TEST_DIR/unpacked" -mindepth 1 -maxdepth 1 -type d | head -n 1)
    [ -f "$root/db_v2.sqlite3" ] || fail "the database is not in the backup"
    [ -f "$root/.api_key" ] || fail ".api_key is not in the backup"
    [ -f "$root/.admin_credentials" ] || fail ".admin_credentials is not in the backup"
    [ -f "$root/console-data/auth.db" ] || fail "the web console's data volume is not in the backup"
    rm -rf "$TEST_DIR"
)

# An install from before the web console moved into the server binary has
# a web-nodejs folder with the manager's .env. An update from a release
# without web-nodejs leaves the folder as it is, after a good build and
# after one that fails, so the next update still takes the channel in it.
test_update_keeps_old_web_nodejs() (
    load
    write_old_tree
    local old="$SCRIPT_DIR/web-nodejs"
    mkdir -p "$old/data" "$old/node_modules"
    printf 'UPDATE_GITHUB_BRANCH=dev\nPUID=1500\n' > "$old/.env"
    printf 'sha\n' > "$old/data/.update_sha"
    printf 'kept\n' > "$old/node_modules/dep.js"
    COMPOSE_CMD=fake_compose
    MENU_CHOICE=1
    menu_choose() { MENU_CHOICE=1; }
    do_backup_silent() { :; }
    local build
    for build in 0 1 0; do
        export BUILD_FAILS=$build
        ( do_update ) >/dev/null 2>&1
        grep -qx 'UPDATE_GITHUB_BRANCH=dev' "$old/.env" 2>/dev/null \
            || fail "web-nodejs/.env lost the update channel after an update (build failed: $build)"
        grep -qx 'PUID=1500' "$old/.env" || fail "web-nodejs/.env lost PUID after an update (build failed: $build)"
        [ -f "$old/data/.update_sha" ] || fail "web-nodejs/data lost a file after an update (build failed: $build)"
        [ -f "$old/node_modules/dep.js" ] || fail "web-nodejs/node_modules lost a file after an update (build failed: $build)"
    done
    [ "$(tr '\n' ' ' < "$TEST_DIR/cloned-branches")" = "dev dev dev " ] \
        || fail "the updates cloned the branches $(tr '\n' ' ' < "$TEST_DIR/cloned-branches")"
    [ "$(cat "$SCRIPT_DIR/betterdesk-server/go.mod")" = new ] || fail "the update did not put the new server source in place"
    rm -rf "$TEST_DIR"
)

test_failed_build_restores_sources
test_backup_keeps_dotfiles_and_console_data
test_update_keeps_old_web_nodejs
echo "Docker installer checks passed"
