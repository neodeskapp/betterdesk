#!/usr/bin/env bash
# Checks of betterdesk-docker.sh without Docker: the manager's functions
# are loaded without its main, and docker, curl, ss and id are stand-ins
# that keep their state in files of a temporary folder. At the end the
# checks of the image and compose files run (test-docker-entrypoint.sh).
#
#   bash scripts/test-docker-installer.sh
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

# load: the manager's functions with the install folder in a temporary one
# and Docker, the network and the clock replaced.
load() {
    set --
    BETTERDESK_DOCKER_SOURCED=1
    # shellcheck source=../betterdesk-docker.sh
    source "$SCRIPT_ROOT/betterdesk-docker.sh"
    set +eu
    TEST_DIR=$(mktemp -d)
    BETTERDESK_DIR="$TEST_DIR/install"
    CALLS="$TEST_DIR/calls"
    HEALTH_TIMEOUT=4
    RED="" GREEN="" YELLOW="" CYAN="" BOLD="" NC=""
    : > "$CALLS"
    printf 'sha256:old\n' > "$TEST_DIR/good-image"
    printf 'sha256:old\n' > "$TEST_DIR/pulled"
    sleep() { :; }
    id() { printf '0\n'; }
}

# docker: what the manager asks of Docker, recorded in $CALLS. State:
# exists, running (the container), current-image, pulled (the image the
# tag names), dir (the container's compose folder).
docker() {
    printf '%s\n' "$*" >> "$CALLS"
    local arg format="" prev="" out="" file=""
    for arg in "$@"; do
        [ "$prev" = -f ] && format="$arg"
        [ "$prev" = -v ] && [[ "$arg" == *:/out ]] && out="${arg%:/out}"
        [[ "$arg" == /out/* ]] && file="${arg#/out/}"
        prev="$arg"
    done
    case "$1" in
        info) return 0 ;;
        compose)
            case "$2" in
                version) [ "${3:-}" = --short ] && printf '2.29.1\n'; return 0 ;;
                pull) return 0 ;;
                up)
                    touch "$TEST_DIR/exists" "$TEST_DIR/running"
                    cp "$TEST_DIR/pulled" "$TEST_DIR/current-image" ;;
                stop|down) rm -f "$TEST_DIR/running" ;;
                start) touch "$TEST_DIR/running" ;;
                config) [ "${3:-}" = --hash ] && printf 'betterdesk newhash\n' ;;
                run)
                    printf '%s\n' "${DATABASE_URL:-}" > "$TEST_DIR/run-env"
                    if [ -f "$TEST_DIR/move-fails" ]; then printf 'ERROR: the move broke\n' >&2; return 1; fi
                    printf 'Migration complete: 12 rows in 30 tables moved to PostgreSQL\n' ;;
            esac
            return 0 ;;
        container)
            if [ "${!#}" = betterdesk-postgres ]; then
                [[ "$format" != *Health* ]] || printf 'healthy\n'
                return 0
            fi
            [ -f "$TEST_DIR/exists" ] && [ "${!#}" = betterdesk ] || return 1
            case "$format" in
                *State.Running*) if [ -f "$TEST_DIR/running" ]; then printf 'true\n'; else printf 'false\n'; fi ;;
                *working_dir*) cat "$TEST_DIR/dir" 2>/dev/null || printf '%s\n' "$BETTERDESK_DIR" ;;
                *config-hash*) printf 'oldhash\n' ;;
                *Config.Image*) printf 'ghcr.io/trybetterdesk/betterdesk:latest\n' ;;
                *Image*) cat "$TEST_DIR/current-image" ;;
            esac
            return 0 ;;
        image)
            [ "$2" = inspect ] || return 0
            [ -z "$format" ] || cat "$TEST_DIR/pulled"
            return 0 ;;
        exec)
            case "$*" in
                *-version*) printf 'betterdesk-server 3.5.270 (built test)\n' ;;
                *.admin_credentials*)
                    [ ! -f "$TEST_DIR/first-password" ] || printf 'Admin Username: admin\nAdmin Password: %s\n' "$(cat "$TEST_DIR/first-password")" ;;
                *id_ed25519.pub*) printf 'PUBKEYbase64=\n' ;;
                *reset-password*) cat > /dev/null ;;
            esac
            return 0 ;;
        run)
            [ -z "$out" ] || [ -z "$file" ] || : > "$out/$file"
            return 0 ;;
        tag) cut -d' ' -f1 <<< "$2" > "$TEST_DIR/pulled"; return 0 ;;
        volume) return 1 ;;
        logs) printf 'the log\n'; return 0 ;;
    esac
    return 0
}

# curl: ghcr.io answers, the public address is 203.0.113.10, and the health
# URLs answer while the container runs the good image
curl() {
    local url="${!#}"
    case "$url" in
        *ghcr.io/v2/*) return 0 ;;
        *checkip*|*ipify*) printf '203.0.113.10\n' ;;
        *health*)
            [ -f "$TEST_DIR/running" ] && [ "$(cat "$TEST_DIR/current-image" 2>/dev/null)" = "$(cat "$TEST_DIR/good-image")" ] ;;
        *) return 1 ;;
    esac
}

# ss: a port is taken when $TEST_DIR/taken-<port> holds the program's name
ss() {
    local port="${2##*:}"
    [ -f "$TEST_DIR/taken-$port" ] || return 0
    printf 'LISTEN 0 4096 0.0.0.0:%s 0.0.0.0:* users:(("%s",pid=7,fd=3))\n' "$port" "$(cat "$TEST_DIR/taken-$port")"
}

# The stand-in for Docker, callable from a test's own docker()
eval "command_docker() $(declare -f docker | tail -n +2)"

# A fresh install writes the settings and the release's compose file,
# starts the container, waits for it and prints the console address, the
# first password once and the client settings. No file of the install
# folder holds the password, and running the script again prints none.
test_fresh_install() (
    load
    printf 'first-pass-0123\n' > "$TEST_DIR/first-password"
    local out
    out=$( (COMMAND=install; do_install) 2>&1 ) || fail "a fresh install failed: $out"
    grep -qx 'RELAY_SERVERS=203.0.113.10' "$BETTERDESK_DIR/.env" || fail ".env has no relay address"
    cmp -s "$BETTERDESK_DIR/docker-compose.yml" "$SCRIPT_ROOT/docker-compose.quick.single.yml" || fail "the compose file is not the release's"
    grep -qx 'compose up -d --remove-orphans' "$CALLS" || fail "the container was not started: $(cat "$CALLS")"
    printf '%s' "$out" | grep -q 'Web console: *http://203.0.113.10:5000' || fail "no console address in: $out"
    printf '%s' "$out" | grep -q 'ID server: *203.0.113.10' || fail "no ID server in: $out"
    printf '%s' "$out" | grep -q 'Key: *PUBKEYbase64=' || fail "no key in: $out"
    printf '%s' "$out" | grep -q 'docker compose logs -f betterdesk' || fail "no logs hint in: $out"
    [ "$(printf '%s' "$out" | grep -c 'first-pass-0123')" = 1 ] || fail "the first password is not printed exactly once: $out"
    if grep -rq 'first-pass-0123' "$BETTERDESK_DIR"; then fail "a file in the install folder holds the password"; fi

    out=$( (COMMAND=install; do_install) 2>&1 ) || fail "the second run failed: $out"
    if printf '%s' "$out" | grep -q 'first-pass-0123'; then fail "the second run printed the password again"; fi
    rm -rf "$TEST_DIR"
)

# An update backs the volumes up, with the container stopped, before the
# new container starts; when the new image does not get healthy, the
# previous one runs again.
test_update_goes_back_when_unhealthy() (
    load
    (COMMAND=install; do_install) > /dev/null 2>&1 || fail "the install failed"
    docker() {
        if [ "$1" = compose ] && [ "$2" = pull ]; then
            printf '%s\n' "$*" >> "$CALLS"
            printf 'sha256:new\n' > "$TEST_DIR/pulled"
            return 0
        fi
        command_docker "$@"
    }
    : > "$CALLS"
    local out stop_line backup_line
    if out=$( (COMMAND=update; do_update) 2>&1 ); then fail "an update to an unhealthy image reported success: $out"; fi
    printf '%s' "$out" | grep -q 'runs the previous version again' || fail "no word of the way back: $out"
    grep -q '^tag sha256:old ' "$CALLS" || fail "the previous image was not tagged again: $(cat "$CALLS")"
    grep -q 'compose up -d --force-recreate betterdesk' "$CALLS" || fail "the container was not made again from the previous image"
    [ "$(cat "$TEST_DIR/current-image")" = sha256:old ] || fail "the container does not run the previous image"
    ls "$BETTERDESK_DIR"/backups/betterdesk-*.tar.gz > /dev/null 2>&1 || fail "no backup before the update"
    stop_line=$(grep -n 'compose stop' "$CALLS" | head -n 1 | cut -d: -f1)
    backup_line=$(grep -n '^run .*tar' "$CALLS" | head -n 1 | cut -d: -f1)
    { [ -n "$stop_line" ] && [ -n "$backup_line" ] && [ "$stop_line" -lt "$backup_line" ]; } \
        || fail "the backup was not taken with the container stopped"
    rm -rf "$TEST_DIR"
)

# L125: a compose file changed by hand stays on update; the release's file
# goes next to it.
test_update_keeps_compose_edits() (
    load
    (COMMAND=install; do_install) > /dev/null 2>&1 || fail "the install failed"
    printf '# my change\n' >> "$BETTERDESK_DIR/docker-compose.yml"
    write_compose_file 2> /dev/null
    grep -q '^# my change' "$BETTERDESK_DIR/docker-compose.yml" || fail "the update overwrote a changed compose file"
    cmp -s "$BETTERDESK_DIR/docker-compose.yml.new" "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
        || fail "the release's compose file is not next to it"
    rm -rf "$TEST_DIR"
)

# L125: a new install with --postgres runs on PostgreSQL: the settings name
# it, and the made-up password is printed nowhere.
test_fresh_install_on_postgres() (
    load
    USE_POSTGRES=true
    local out pass
    out=$( (COMMAND=install; do_install) 2>&1 ) || fail "a PostgreSQL install failed: $out"
    pass=$(env_get POSTGRES_PASSWORD)
    [ "${#pass}" -ge 32 ] || fail "no PostgreSQL password made"
    [ "$(env_get DATABASE_URL)" = "postgres://betterdesk:${pass}@postgres:5432/betterdesk?sslmode=disable" ] \
        || fail "DATABASE_URL is $(env_get DATABASE_URL)"
    [ "$(env_get COMPOSE_PROFILES)" = postgres ] || fail "the postgres service is not on"
    if printf '%s' "$out" | grep -qF "$pass"; then fail "the PostgreSQL password was printed"; fi
    grep -qE '^  postgres:' "$BETTERDESK_DIR/docker-compose.yml" || fail "the compose file has no postgres service"
    rm -rf "$TEST_DIR"
)

# BetterDesk from another install (an older manager, a split install) is
# not touched: the error says how to take it over.
test_foreign_install_is_left_alone() (
    load
    touch "$TEST_DIR/exists" "$TEST_DIR/running"
    printf '/root/old-betterdesk\n' > "$TEST_DIR/dir"
    local out
    if out=$( (COMMAND=install; do_install) 2>&1 ); then fail "an install over another install's container went on"; fi
    printf '%s' "$out" | grep -q -- '--adopt' || fail "the error does not name --adopt: $out"
    [ ! -e "$BETTERDESK_DIR/.env" ] || fail "the install folder was written although another install runs"
    if grep -qE '^compose (up|stop|down)|^stop |^rm ' "$CALLS"; then fail "the other install's container was touched"; fi
    rm -rf "$TEST_DIR"
)

# Errors say what to do: a port taken, Compose v1 only, no Docker, no
# sudo, no internet.
test_errors_say_what_to_do() (
    load
    local out
    printf 'python3\n' > "$TEST_DIR/taken-5000"
    if out=$( (COMMAND=install; do_install) 2>&1 ); then fail "an install with port 5000 taken went on"; fi
    printf '%s' "$out" | grep -q 'TCP 5000: python3' || fail "the port error does not name the program: $out"
    printf '%s' "$out" | grep -q -- '--console-port' || fail "the port error does not name --console-port: $out"
    rm -f "$TEST_DIR/taken-5000"
    printf 'hbbs\n' > "$TEST_DIR/taken-21116"
    if out=$( (COMMAND=install; do_install) 2>&1 ); then fail "an install with 21116 taken went on"; fi
    printf '%s' "$out" | grep -q 'RustDesk server' || fail "the error does not name the RustDesk server: $out"
    rm -f "$TEST_DIR/taken-21116"

    if out=$( docker() { case "$1" in info) return 0 ;; *) return 1 ;; esac; }
              docker-compose() { :; }
              check_docker 2>&1 ); then fail "Compose v1 alone passed the check"; fi
    printf '%s' "$out" | grep -q 'docker-compose-plugin' || fail "the Compose error does not say how to install v2: $out"

    if out=$( unset -f docker; PATH="$TEST_DIR/empty"; INSTALL_DOCKER=false; check_docker 2>&1 ); then
        fail "a missing Docker passed the check"
    fi
    printf '%s' "$out" | grep -q 'get.docker.com' || fail "the Docker error does not say how to install it: $out"

    if out=$( id() { printf '1000\n'; }; docker() { return 1; }; need_root_or_docker 2>&1 ); then
        fail "a user without root went on"
    fi
    printf '%s' "$out" | grep -q 'sudo' || fail "the root error does not name sudo: $out"

    if out=$( curl() { return 6; }; check_internet 2>&1 ); then fail "no internet passed the check"; fi
    printf '%s' "$out" | grep -q -- '--no-pull' || fail "the internet error does not say what to do: $out"
    rm -rf "$TEST_DIR"
)

# Uninstall says what it leaves; --purge without --yes and without a
# terminal deletes nothing.
test_uninstall_says_what_it_leaves() (
    load
    (COMMAND=install; do_install) > /dev/null 2>&1 || fail "the install failed"
    local out
    out=$(do_uninstall 2>&1) || fail "uninstall failed: $out"
    printf '%s' "$out" | grep -q 'betterdesk-data' || fail "uninstall does not name the volumes it leaves: $out"
    printf '%s' "$out" | grep -q 'docker image rm' || fail "uninstall does not name the image it leaves: $out"
    if grep -q 'down -v' "$CALLS"; then fail "uninstall without --purge deleted the volumes"; fi
    PURGE=true
    if out=$(do_uninstall 2>&1 < /dev/null); then fail "--purge without --yes went on"; fi
    if grep -q 'down -v' "$CALLS"; then fail "--purge without --yes deleted the volumes"; fi
    rm -rf "$TEST_DIR"
)

# --help names every option the script takes.
test_help_lists_every_option() (
    local opt help
    help=$(bash "$SCRIPT_ROOT/betterdesk-docker.sh" --help)
    for opt in $(sed -n '/^parse_args() {/,/^}/p' "$SCRIPT_ROOT/betterdesk-docker.sh" | grep -oE '^ +--[a-z-]+' | tr -d ' ' | sort -u); do
        printf '%s' "$help" | grep -q -- "$opt" || fail "--help does not list $opt"
    done
)

# L43: the switch to PostgreSQL stops BetterDesk, backs the volumes up,
# starts the postgres service, moves the data with the server's sqlite2pg
# and starts BetterDesk on PostgreSQL. The database address reaches the
# tool through the environment, never the command line.
test_postgres_switch_moves_the_data() (
    load
    (COMMAND=install; do_install) > /dev/null 2>&1 || fail "the install failed"
    : > "$CALLS"
    ASSUME_YES=true
    local out pass stop_line run_line
    out=$(do_postgres 2>&1) || fail "the switch failed: $out"
    pass=$(env_get POSTGRES_PASSWORD)
    [ "${#pass}" -ge 32 ] || fail "no PostgreSQL password made"
    [ "$(env_get DATABASE_URL)" = "postgres://betterdesk:${pass}@postgres:5432/betterdesk?sslmode=disable" ] \
        || fail "DATABASE_URL after the switch: $(env_get DATABASE_URL)"
    [ "$(env_get COMPOSE_PROFILES)" = postgres ] || fail "the postgres service is not on after the switch"
    grep -q 'compose run --rm --no-deps -T --user 10001:10001 --entrypoint sh betterdesk -c exec /usr/local/bin/betterdesk-migrate -mode sqlite2pg -src /opt/rustdesk/db_v2.sqlite3' "$CALLS" \
        || fail "the data was not moved with sqlite2pg: $(cat "$CALLS")"
    [ "$(cat "$TEST_DIR/run-env")" = "$(env_get DATABASE_URL)" ] || fail "the tool did not get the address in its environment"
    if grep -qF "$pass" "$CALLS"; then fail "the PostgreSQL password is on a command line"; fi
    if printf '%s' "$out" | grep -qF "$pass"; then fail "the PostgreSQL password was printed"; fi
    stop_line=$(grep -n 'compose stop -t 30 betterdesk' "$CALLS" | head -n 1 | cut -d: -f1)
    run_line=$(grep -n 'compose run' "$CALLS" | head -n 1 | cut -d: -f1)
    { [ -n "$stop_line" ] && [ "$stop_line" -lt "$run_line" ]; } || fail "the data was moved while BetterDesk ran"
    grep -q '^run .*tar' "$CALLS" || fail "no backup before the move"
    printf '%s' "$out" | grep -q 'Migration complete: 12 rows' || fail "the tool's result is not shown: $out"
    rm -rf "$TEST_DIR"
)

# L43, decision 26: declining the move keeps the install on SQLite and
# changes nothing.
test_postgres_declined_keeps_sqlite() (
    load
    (COMMAND=install; do_install) > /dev/null 2>&1 || fail "the install failed"
    : > "$CALLS"
    MOVE_DATA=no
    local out
    out=$(do_postgres 2>&1) || fail "declining failed: $out"
    printf '%s' "$out" | grep -q 'stays on SQLite' || fail "declining does not say the install stays on SQLite: $out"
    [ -z "$(env_get DATABASE_URL)" ] || fail "declining set DATABASE_URL"
    if grep -qE '^compose (stop|run|--profile postgres up)' "$CALLS"; then fail "declining touched the containers: $(cat "$CALLS")"; fi
    rm -rf "$TEST_DIR"
)

# L43: a move that fails leaves the install on SQLite and starts it again.
test_postgres_failed_move_keeps_sqlite() (
    load
    (COMMAND=install; do_install) > /dev/null 2>&1 || fail "the install failed"
    touch "$TEST_DIR/move-fails"
    ASSUME_YES=true
    local out
    if out=$(do_postgres 2>&1); then fail "a failed move reported success: $out"; fi
    printf '%s' "$out" | grep -q 'stays on SQLite' || fail "the error does not say the install stays on SQLite: $out"
    [ -z "$(env_get DATABASE_URL)" ] || fail "a failed move left DATABASE_URL set"
    [ -z "$(env_get COMPOSE_PROFILES)" ] || fail "a failed move left the postgres service on"
    [ -f "$TEST_DIR/running" ] || fail "BetterDesk was not started again on SQLite"
    rm -rf "$TEST_DIR"
)

test_fresh_install
test_update_goes_back_when_unhealthy
test_update_keeps_compose_edits
test_fresh_install_on_postgres
test_foreign_install_is_left_alone
test_errors_say_what_to_do
test_uninstall_says_what_it_leaves
test_help_lists_every_option
test_postgres_switch_moves_the_data
test_postgres_declined_keeps_sqlite
test_postgres_failed_move_keeps_sqlite
echo "Docker installer checks passed"
bash "$SCRIPT_ROOT/scripts/test-docker-entrypoint.sh"
