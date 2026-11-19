#!/usr/bin/env bash
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_TAG="${BETTERDESK_IMAGE_TAG:-dev}"
IMAGE="${BETTERDESK_HARDENING_IMAGE:-ghcr.io/unitronix/betterdesk:${IMAGE_TAG}}"
PROJECT="betterdesk-hardening-$RANDOM"
TEST_DIR="$(mktemp -d)"
OVERRIDE_FILE="$TEST_DIR/compose.override.yml"
DOCKER_BIN="${BETTERDESK_DOCKER_BIN:-}"
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
            -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
            -f "$OVERRIDE_FILE" logs --tail=120 >&2 || true
        docker compose -p "$PROJECT" \
            -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
            -f "$OVERRIDE_FILE" exec -T -u betterdesk betterdesk \
            sh -c 'ls -la /opt/rustdesk; sqlite3 /opt/rustdesk/db_v2.sqlite3 "SELECT COUNT(*) FROM users;"' >&2 || true
        ls -la "$TEST_DIR/rustdesk" >&2 || true
    fi
    exit 1
}

# The server made the first admin: the database has a user
wait_for_bootstrap_admin() {
    local users
    for _ in {1..60}; do
        users=$(docker compose -p "$PROJECT" \
            -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
            -f "$OVERRIDE_FILE" exec -T -u betterdesk betterdesk \
            sqlite3 /opt/rustdesk/db_v2.sqlite3 "SELECT COUNT(*) FROM users;" 2>/dev/null | tr -d '\r\n' || true)
        if [ -n "$users" ] && [ "$users" -gt 0 ] 2>/dev/null; then
            return 0
        fi
        sleep 1
    done
    fail "container did not bootstrap an admin user"
}

cleanup() {
    docker compose -p "$PROJECT" \
        -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
        -f "$OVERRIDE_FILE" down -v --remove-orphans >/dev/null 2>&1 || true
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

if [[ -z "$DOCKER_BIN" ]] || ! docker info >/dev/null 2>&1; then
    printf 'SKIP: Docker is not available\n'
    exit 0
fi

mkdir -p "$TEST_DIR/rustdesk" "$TEST_DIR/console"
cat > "$OVERRIDE_FILE" <<EOF
services:
  betterdesk:
    image: ${IMAGE}
    volumes:
      - "${DOCKER_TEST_DIR}/rustdesk:/opt/rustdesk"
      - "${DOCKER_TEST_DIR}/console:/app/data"
EOF

if [[ "$IMAGE" == ghcr.io/* ]]; then
    docker compose -p "$PROJECT" \
        -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
        -f "$OVERRIDE_FILE" pull betterdesk
fi

printf 'stable-test-api-key\n' > "$TEST_DIR/rustdesk/.api_key"
chmod 600 "$TEST_DIR/rustdesk/.api_key"
docker run --rm \
    --entrypoint sh \
    -v "$TEST_DIR/rustdesk:/opt/rustdesk" \
    -v "$TEST_DIR/console:/app/data" \
    "$IMAGE" \
    -c 'chown -R 10001:10001 /opt/rustdesk /app/data && chmod 700 /opt/rustdesk /app/data'

docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" up -d --wait

wait_for_bootstrap_admin

first_key=$(docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" exec -T -u betterdesk betterdesk \
    sh -c 'cat /opt/rustdesk/.api_key' | tr -d '\r\n')
[ "$first_key" = "stable-test-api-key" ] \
    || fail "existing API key was not preserved on first start"

docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" restart betterdesk
docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" up -d --wait

second_key=$(docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" exec -T -u betterdesk betterdesk \
    sh -c 'cat /opt/rustdesk/.api_key' | tr -d '\r\n')
[ "$second_key" = "$first_key" ] \
    || fail "API key changed after a hardened-container restart"

docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" exec -T -u betterdesk betterdesk \
    sh -c 'rm -f /opt/rustdesk/.admin_credentials'
[ ! -e "$TEST_DIR/rustdesk/.admin_credentials" ] \
    || fail "credentials file could not be removed from the bind mount"
docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" restart betterdesk
docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" up -d --wait

[ ! -e "$TEST_DIR/rustdesk/.admin_credentials" ] \
    || fail "bootstrap recreated credentials for an existing database"
# One process: betterdesk-server as betterdesk under tini, nothing else
processes=$(docker compose -p "$PROJECT" \
    -f "$SCRIPT_ROOT/docker-compose.quick.single.yml" \
    -f "$OVERRIDE_FILE" exec -T betterdesk ps -o user,args | tr -d '\r')
printf '%s\n' "$processes" | grep -Eq '^betterde[a-z]* +/usr/local/bin/betterdesk-server ' \
    || fail "betterdesk-server does not run as betterdesk: $processes"
if printf '%s\n' "$processes" | grep -Eq 'node|supervisord'; then
    fail "the container runs more than the server: $processes"
fi

printf 'PASS: hardened Docker startup regression tests\n'
