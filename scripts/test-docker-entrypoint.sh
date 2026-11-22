#!/usr/bin/env bash
# Checks of the image's start scripts (docker/), the Dockerfiles and the
# compose files, without Docker.
#
#   bash scripts/test-docker-entrypoint.sh
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

# L34: a database address in a log line shows where the database is and
# nothing of the credentials: no user name, no password, no query string
# (which may carry password=).
test_dsn_for_log_drops_credentials() (
    # shellcheck source=/dev/null
    . "$SCRIPT_ROOT/docker/dsn.sh"
    local got
    got=$(dsn_for_log 'postgres://bdesk:S3cr3tPw@postgres:5432/betterdesk?sslmode=disable')
    [ "$got" = 'postgres://postgres:5432/betterdesk' ] || fail "dsn_for_log gave '$got'"
    got=$(dsn_for_log 'postgresql://bdesk:p@ss:w0rd@db.example.net/bd')
    [ "$got" = 'postgresql://db.example.net/bd' ] || fail "a password with @ and : gave '$got'"
    got=$(dsn_for_log 'postgres://bdesk@db:5432/betterdesk?password=Hunter2')
    [ "$got" = 'postgres://db:5432/betterdesk' ] || fail "a password in the query gave '$got'"
    got=$(dsn_for_log 'host=db user=bdesk password=Hunter2')
    case "$got" in *Hunter2*|*bdesk*) fail "a key=value DSN gave '$got'" ;; esac
    got=$(dsn_for_log '/opt/rustdesk/db_v2.sqlite3')
    [ "$got" = '/opt/rustdesk/db_v2.sqlite3' ] || fail "a SQLite path gave '$got'"
    [ "$(dsn_host 'postgres://bdesk:p@ss:5@postgres:5433/bd')" = postgres ] || fail "dsn_host of a password with @"
    [ "$(dsn_port 'postgres://bdesk:p@ss:5@postgres:5433/bd')" = 5433 ] || fail "dsn_port of a password with @"
    [ "$(dsn_port 'postgres://bdesk:secret@postgres/bd')" = 5432 ] || fail "dsn_port without a port"
    [ "$(dsn_host 'postgres://bdesk:secret@[fd00::5]:5432/bd')" = 'fd00::5' ] || fail "dsn_host of an IPv6 address"
)

# L34: no start script prints a database address or password itself;
# every address goes through dsn_for_log.
test_scripts_print_no_raw_dsn() (
    local hits
    hits=$(grep -nE '(echo|printf).*\$\{?(DATABASE_URL|DB_URL|POSTGRESQL_URI|POSTGRESQL_PASS|POSTGRES_PASSWORD|PG_PASSWORD)' \
        "$SCRIPT_ROOT"/docker/*.sh "$SCRIPT_ROOT/betterdesk-docker.sh" | grep -v 'dsn_for_log' || true)
    [ -z "$hits" ] || fail "a script prints a database address or password: $hits"
)

# The image copies dsn.sh next to the entrypoint, which sources it.
test_image_has_dsn_helper() (
    grep -q 'COPY docker/dsn.sh /docker/dsn.sh' "$SCRIPT_ROOT/Dockerfile" \
        || fail "the Dockerfile does not copy docker/dsn.sh"
    grep -q '^\. /docker/dsn.sh' "$SCRIPT_ROOT/docker/entrypoint.sh" \
        || fail "docker/entrypoint.sh does not source /docker/dsn.sh"
)

# L35: the start scripts pass ENROLLMENT_MODE on as the first default and
# never mark it as forced; ENROLLMENT_MODE_ENV_OVERRIDE is the admin's to
# set, and every compose file passes it through.
test_enrollment_mode_is_not_forced() (
    local hits f
    hits=$(grep -nE '^[^#]*ENROLLMENT_MODE_ENV_OVERRIDE=' "$SCRIPT_ROOT"/docker/*.sh || true)
    [ -z "$hits" ] || fail "a start script sets ENROLLMENT_MODE_ENV_OVERRIDE: $hits"
    for f in "$SCRIPT_ROOT"/docker-compose*.yml; do
        grep -q 'ENROLLMENT_MODE_ENV_OVERRIDE=${ENROLLMENT_MODE_ENV_OVERRIDE:-}' "$f" \
            || fail "$(basename "$f") does not pass ENROLLMENT_MODE_ENV_OVERRIDE through"
    done
)

# The compose file the docs show first runs the published image, builds
# nothing and follows releases; every compose file says which one is which.
test_quick_compose_pulls_the_published_image() (
    local quick="$SCRIPT_ROOT/docker-compose.quick.single.yml" f other
    grep -q 'image: ghcr.io/trybetterdesk/betterdesk:${BETTERDESK_IMAGE_TAG:-latest}' "$quick"         || fail "docker-compose.quick.single.yml does not run ghcr.io/trybetterdesk/betterdesk:latest"
    if grep -qE '^[[:space:]]+build:' "$quick"; then fail "docker-compose.quick.single.yml builds an image"; fi
    if grep -qE 'pull_policy:[[:space:]]*never' "$quick"; then fail "docker-compose.quick.single.yml never pulls"; fi
    for f in "$SCRIPT_ROOT"/docker-compose*.yml; do
        for other in docker-compose.quick.single.yml docker-compose.quick.single.macvlan.yml docker-compose.single.yml; do
            sed -n '1,20p' "$f" | grep -q "$other" || fail "the comment at the top of $(basename "$f") does not name $other"
        done
        if grep -qi 'ghcr.io/unitronix' "$f"; then fail "$(basename "$f") names the upstream image"; fi
    done
)

test_dsn_for_log_drops_credentials
test_scripts_print_no_raw_dsn
test_image_has_dsn_helper
test_enrollment_mode_is_not_forced
test_quick_compose_pulls_the_published_image
echo "Docker entrypoint checks passed"
