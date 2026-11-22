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
    grep -q 'image: ghcr.io/trybetterdesk/betterdesk:${BETTERDESK_IMAGE_TAG:-latest}' "$quick" || fail "docker-compose.quick.single.yml does not run ghcr.io/trybetterdesk/betterdesk:latest"
    if grep -qE '^[[:space:]]+build:' "$quick"; then fail "docker-compose.quick.single.yml builds an image"; fi
    if grep -qE 'pull_policy:[[:space:]]*never' "$quick"; then fail "docker-compose.quick.single.yml never pulls"; fi
    for f in "$SCRIPT_ROOT"/docker-compose*.yml; do
        for other in docker-compose.quick.single.yml docker-compose.quick.single.macvlan.yml docker-compose.single.yml; do
            sed -n '1,20p' "$f" | grep -q "$other" || fail "the comment at the top of $(basename "$f") does not name $other"
        done
        if grep -qi 'ghcr.io/unitronix' "$f"; then fail "$(basename "$f") names the upstream image"; fi
    done
)

# L79: the CDAP gateway listens on 21122 by default (A116), so the images
# expose it and the compose files with host ports publish it.
test_cdap_port_is_published() (
    local f
    for f in docker-compose.quick.single.yml docker-compose.single.yml; do
        grep -qE '^[[:space:]]+- "21122:21122"' "$SCRIPT_ROOT/$f" || fail "$f does not publish 21122"
    done
    for f in Dockerfile Dockerfile.server; do
        grep -qE '^EXPOSE .* 21122( |$)' "$SCRIPT_ROOT/$f" || fail "$f does not expose 21122"
    done
)

# L80: every compose file reads .env next to it (when there is one), so a
# setting needs no edit of the compose file, and the host address the
# console's ports are published on is a variable.
test_compose_reads_env_file() (
    local f
    for f in "$SCRIPT_ROOT"/docker-compose*.yml; do
        grep -A2 -E '^[[:space:]]+env_file:' "$f" | grep -qE 'path: \.env' || fail "$(basename "$f") does not read .env"
        grep -A2 -E '^[[:space:]]+env_file:' "$f" | grep -qE 'required: false' || fail "$(basename "$f") needs a .env to start"
    done
    for f in docker-compose.quick.single.yml docker-compose.single.yml; do
        grep -qF '"${BETTERDESK_CONSOLE_BIND:-0.0.0.0}:${BETTERDESK_CONSOLE_PORT:-5000}:5000"' "$SCRIPT_ROOT/$f" || fail "$f fixes the console's bind address"
    done
)

# L81: the health check reaches the API over HTTPS when TLS_API=Y and the
# console over HTTPS when HTTPS_ENABLED=true; the images and the compose
# files use it.
test_healthcheck_follows_tls() (
    local dir out
    dir=$(mktemp -d)
    # curl that answers only the URLs listed in $dir/up
    cat > "$dir/curl" <<'STUB'
#!/bin/sh
for a in "$@"; do url="$a"; done
grep -qxF "$url" "$(dirname "$0")/up"
STUB
    chmod +x "$dir/curl"
    printf '%s\n' https://127.0.0.1:21121/api/health http://127.0.0.1:5000/health > "$dir/up"
    PATH="$dir:$PATH" TLS_API=Y sh "$SCRIPT_ROOT/docker/healthcheck.sh" || fail "unhealthy with TLS_API=Y and the API on HTTPS"
    printf '%s\n' http://127.0.0.1:21121/api/health https://127.0.0.1:5443/health > "$dir/up"
    PATH="$dir:$PATH" HTTPS_ENABLED=true sh "$SCRIPT_ROOT/docker/healthcheck.sh" || fail "unhealthy with HTTPS_ENABLED=true and the console on 5443"
    printf '%s\n' http://127.0.0.1:21121/api/health > "$dir/up"
    if PATH="$dir:$PATH" sh "$SCRIPT_ROOT/docker/healthcheck.sh"; then fail "healthy without the console"; fi
    PATH="$dir:$PATH" CONSOLE_PORT=0 sh "$SCRIPT_ROOT/docker/healthcheck.sh" || fail "unhealthy with the console off"
    : > "$dir/up"
    if PATH="$dir:$PATH" CONSOLE_PORT=0 sh "$SCRIPT_ROOT/docker/healthcheck.sh"; then fail "healthy without the API"; fi
    rm -rf "$dir"
    for out in Dockerfile Dockerfile.server; do
        grep -qE '^[[:space:]]+CMD /usr/local/bin/betterdesk-healthcheck' "$SCRIPT_ROOT/$out" || fail "$out does not use the health check script"
        grep -q 'COPY docker/healthcheck.sh /usr/local/bin/betterdesk-healthcheck' "$SCRIPT_ROOT/$out" || fail "$out does not copy the health check script"
        grep -qE '^RUN chmod [+]x .*/usr/local/bin/betterdesk-healthcheck' "$SCRIPT_ROOT/$out" || fail "$out does not make the health check script executable"
    done
    for out in "$SCRIPT_ROOT"/docker-compose*.yml; do
        if grep -A1 -E '^[[:space:]]+healthcheck:' "$out" | grep -q 'curl'; then fail "$(basename "$out") checks health with plain curl"; fi
    done
)

# L82: the images' binary knows its version, build date and commit: the
# Dockerfiles pass all three to the linker (the version from VERSION and
# the date of the build when no build argument names them), and the compose
# file that builds passes the commit on.
test_image_binary_knows_its_build() (
    local df
    for df in Dockerfile Dockerfile.server; do
        grep -qF -- '-X main.Commit=${BETTERDESK_COMMIT_SHA}' "$SCRIPT_ROOT/$df" || fail "$df builds without the commit"
        grep -qF -- '-X main.BuildDate=${built}' "$SCRIPT_ROOT/$df" || fail "$df builds without the build date"
        grep -qF -- 'version="${BETTERDESK_PRODUCT_VERSION:-$(tr -d' "$SCRIPT_ROOT/$df" || fail "$df does not take the version from VERSION"
    done
    grep -qE '^[[:space:]]+BETTERDESK_COMMIT_SHA: \$\{BETTERDESK_COMMIT_SHA:-\}' "$SCRIPT_ROOT/docker-compose.single.yml" \
        || fail "docker-compose.single.yml does not pass the commit to the build"
)

# L78: the credentials command's hints name the compose service betterdesk.
test_credentials_hint_names_the_service() (
    if grep -q 'logs server' "$SCRIPT_ROOT/docker/show-admin-credentials.sh"; then
        fail "show-admin-credentials.sh points at a service called server"
    fi
    grep -q 'docker compose logs betterdesk' "$SCRIPT_ROOT/docker/show-admin-credentials.sh" || fail "show-admin-credentials.sh does not point at docker compose logs betterdesk"
)

# L77: after the console removed the first password (it leaves
# .admin_credentials.removed), the start makes no new credentials file,
# with or without ADMIN_PASSWORD; without the note a fresh volume gets one.
test_bootstrap_keeps_a_replaced_first_password_away() (
    local dir out
    dir=$(mktemp -d)
    : > "$dir/.admin_credentials.removed"
    out=$(cd "$dir" && RUSTDESK_PATH="$dir" DB_PATH="$dir/none.sqlite3" sh -c '. "$1"' sh "$SCRIPT_ROOT/docker/bootstrap-admin-credentials.sh" 2>&1)
    [ ! -e "$dir/.admin_credentials" ] || fail "the start made the first password again after it was replaced: $out"
    out=$(cd "$dir" && RUSTDESK_PATH="$dir" DB_PATH="$dir/none.sqlite3" ADMIN_PASSWORD=env-pass-123456 sh -c '. "$1"' sh "$SCRIPT_ROOT/docker/bootstrap-admin-credentials.sh" 2>&1)
    [ ! -e "$dir/.admin_credentials" ] || fail "the start wrote ADMIN_PASSWORD into a credentials file after the first password was replaced: $out"
    rm -f "$dir/.admin_credentials.removed"
    out=$(cd "$dir" && RUSTDESK_PATH="$dir" DB_PATH="$dir/none.sqlite3" sh -c '. "$1"' sh "$SCRIPT_ROOT/docker/bootstrap-admin-credentials.sh" 2>&1)
    grep -q '^Admin Password: ' "$dir/.admin_credentials" 2>/dev/null || fail "a fresh volume got no first password: $out"
    rm -rf "$dir"
)

test_dsn_for_log_drops_credentials
test_scripts_print_no_raw_dsn
test_image_has_dsn_helper
test_enrollment_mode_is_not_forced
test_quick_compose_pulls_the_published_image
test_cdap_port_is_published
test_compose_reads_env_file
test_healthcheck_follows_tls
test_image_binary_knows_its_build
test_credentials_hint_names_the_service
test_bootstrap_keeps_a_replaced_first_password_away
echo "Docker entrypoint checks passed"
