#!/usr/bin/env bash
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_TAG="${NEODESK_IMAGE_TAG:-dev}"
IMAGE="${NEODESK_HARDENING_IMAGE:-ghcr.io/neodeskapp/neodesk:${IMAGE_TAG}}"
PROJECT="neodesk-hardening-$RANDOM"
TEST_DIR="$(mktemp -d)"
OVERRIDE_FILE="$TEST_DIR/compose.override.yml"
DOCKER_BIN="${NEODESK_DOCKER_BIN:-}"
if [[ -z "$DOCKER_BIN" ]]; then
    DOCKER_BIN="$(command -v docker 2>/dev/null || command -v docker.exe 2>/dev/null || true)"
fi

docker() {
    "$DOCKER_BIN" "$@"
}

if command -v cygpath >/dev/null 2>&1; then
    DOCKER_TEST_DIR="$(cygpath -m "$TEST_DIR")"
else
    DOCKER_TEST_DIR="$TEST_DIR"
fi

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    if [[ -f "$OVERRIDE_FILE" ]] && [[ -n "$DOCKER_BIN" ]]; then
        docker compose -p "$PROJECT" \
            -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
            -f "$OVERRIDE_FILE" logs --tail=120 >&2 || true
        docker compose -p "$PROJECT" \
            -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
            -f "$OVERRIDE_FILE" exec -T -u neodesk neodesk \
            sh -c 'ls -la /opt/neodesk/data; sqlite3 /opt/neodesk/data/db_v2.sqlite3 "SELECT COUNT(*) FROM users;"' >&2 || true
        ls -la "$TEST_DIR/data" >&2 || true
    fi
    exit 1
}

# The server made the first admin: the database has a user
wait_for_bootstrap_admin() {
    local users
    for _ in {1..60}; do
        users=$(docker compose -p "$PROJECT" \
            -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
            -f "$OVERRIDE_FILE" exec -T -u neodesk neodesk \
            sqlite3 /opt/neodesk/data/db_v2.sqlite3 "SELECT COUNT(*) FROM users;" 2>/dev/null | tr -d '\r\n' || true)
        if [ -n "$users" ] && [ "$users" -gt 0 ] 2>/dev/null; then
            return 0
        fi
        sleep 1
    done
    fail "container did not bootstrap an admin user"
}

cleanup() {
    docker compose -p "$PROJECT" \
        -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
        -f "$OVERRIDE_FILE" down -v --remove-orphans >/dev/null 2>&1 || true
    # The server's folders belong to uid 10001 with mode 700, so only root
    # in a container can remove them
    if [[ -n "$DOCKER_BIN" ]] && [[ -d "$TEST_DIR/data" ]]; then
        docker run --rm --entrypoint sh -v "$TEST_DIR:/cleanup" "$IMAGE" \
            -c 'rm -rf /cleanup/data /cleanup/console' >/dev/null 2>&1 || true
    fi
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

if [[ -z "$DOCKER_BIN" ]] || ! docker info >/dev/null 2>&1; then
    printf 'SKIP: Docker is not available\n'
    exit 0
fi

mkdir -p "$TEST_DIR/data" "$TEST_DIR/console"
cat > "$OVERRIDE_FILE" <<EOF
services:
  neodesk:
    image: ${IMAGE}
    volumes:
      - "${DOCKER_TEST_DIR}/data:/opt/neodesk/data"
      - "${DOCKER_TEST_DIR}/console:/app/data"
EOF

# A published image is pulled; one built here (scripts/build-test-image.sh)
# is used as it is
if [[ "$IMAGE" == ghcr.io/* ]] && ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
    docker compose -p "$PROJECT" \
        -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
        -f "$OVERRIDE_FILE" pull neodesk
fi

printf 'stable-test-api-key\n' > "$TEST_DIR/data/.api_key"
chmod 600 "$TEST_DIR/data/.api_key"
docker run --rm \
    --entrypoint sh \
    -v "$TEST_DIR/data:/opt/neodesk/data" \
    -v "$TEST_DIR/console:/app/data" \
    "$IMAGE" \
    -c 'chown -R 10001:10001 /opt/neodesk/data /app/data && chmod 700 /opt/neodesk/data /app/data'

docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" up -d --wait

wait_for_bootstrap_admin

first_key=$(docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" exec -T -u neodesk neodesk \
    sh -c 'cat /opt/neodesk/data/.api_key' | tr -d '\r\n')
[ "$first_key" = "stable-test-api-key" ] \
    || fail "existing API key was not preserved on first start"

docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" restart neodesk
docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" up -d --wait

second_key=$(docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" exec -T -u neodesk neodesk \
    sh -c 'cat /opt/neodesk/data/.api_key' | tr -d '\r\n')
[ "$second_key" = "$first_key" ] \
    || fail "API key changed after a hardened-container restart"

docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" exec -T -u neodesk neodesk \
    sh -c 'rm -f /opt/neodesk/data/.admin_credentials'
[ ! -e "$TEST_DIR/data/.admin_credentials" ] \
    || fail "credentials file could not be removed from the bind mount"
docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" restart neodesk
docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" up -d --wait

[ ! -e "$TEST_DIR/data/.admin_credentials" ] \
    || fail "bootstrap recreated credentials for an existing database"
# One process: neodesk-server as neodesk under tini, nothing else
processes=$(docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" exec -T neodesk ps -o user,args | tr -d '\r')
printf '%s\n' "$processes" | grep -Eq '^neodesk +/usr/local/bin/neodesk-server ' \
    || fail "neodesk-server does not run as neodesk: $processes"
if printf '%s\n' "$processes" | grep -Eq 'node|supervisord'; then
    fail "the container runs more than the server: $processes"
fi

# MIGRATE-CLIENT-3: docker stop reaches the server through tini (which
# needs KILL for a process of another user), so it shuts down by itself:
# exit code 0, its own shutdown line, no "FATAL tini"
container=$(docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/install/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" ps -q neodesk)
docker stop -t 20 "$container" >/dev/null
code=$(docker inspect -f '{{.State.ExitCode}}' "$container")
stop_log=$(docker logs --tail 50 "$container" 2>&1)
if printf '%s\n' "$stop_log" | grep -q 'FATAL tini'; then
    fail "tini could not pass SIGTERM on: $stop_log"
fi
[ "$code" = 0 ] || fail "the server ended with $code after docker stop: $stop_log"
printf '%s\n' "$stop_log" | grep -q 'shutting down' || fail "the server did not shut down by itself: $stop_log"

printf 'PASS: hardened Docker startup regression tests\n'
