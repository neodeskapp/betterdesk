#!/usr/bin/env bash
# Unit checks of betterdesk.sh's upgrade to the server with its web console:
# reading the old units, carrying their settings into .env and writing the
# one unit. Runs without systemd: the functions work on files in a
# temporary folder.
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

# load: the installer's functions with every path in a temporary folder.
load() {
    set --
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/betterdesk.sh"
    TEST_DIR=$(mktemp -d)
    SERVER_UNIT="$TEST_DIR/betterdesk-server.service"
    LEGACY_CONSOLE_UNIT="$TEST_DIR/betterdesk-console.service"
    RUSTDESK_PATH="$TEST_DIR/opt-betterdesk"
    CONSOLE_PATH="$TEST_DIR/console"
    mkdir -p "$RUSTDESK_PATH" "$CONSOLE_PATH"
    LOG_FILE=/dev/null
    print_info() { :; }
}

# The units an install with the Node.js console has (betterdesk.sh 3.5.221).
write_node_era_units() {
    cat > "$SERVER_UNIT" <<'UNIT'
[Service]
Type=simple
User=root
EnvironmentFile=-/opt/BetterDeskConsole/.env
Environment=AUTH_DB_PATH=/opt/BetterDeskConsole/data/auth.db
Environment=MESH_ENABLED=Y
Environment=SIGNAL_PORT=21116
Environment=GO_API_PORT=21114
Environment=CONSOLE_PORT=0
Environment="P2P_FIRST=Y"
Environment=P2P_FALLBACK_MS=2000 SAME_NAT_RELAY=Y
ExecStart=/opt/betterdesk/betterdesk-server -mode all -relay-servers 203.0.113.7 -db "postgres://u:p$$w%%d@localhost:5432/bd" -key-file /opt/betterdesk/id_ed25519 -api-port 21121 -signal-rate-limit-per-ip 20 -init-admin-pass S3cr$$t -tls-cert /opt/betterdesk/ssl/betterdesk.crt -tls-key /opt/betterdesk/ssl/betterdesk.key -tls-signal -tls-relay -tls-api
UNIT
    cat > "$LEGACY_CONSOLE_UNIT" <<'UNIT'
[Service]
User=betterdesk
EnvironmentFile=-/opt/BetterDeskConsole/.env
ExecStart=/usr/bin/node server.js
Environment=NODE_ENV=production
Environment=PORT=8080
Environment=HOST=0.0.0.0
Environment=NODE_EXTRA_CA_CERTS=/opt/betterdesk/ssl/betterdesk.crt
Environment=BETTERDESK_SERVER_BINARY=/usr/local/bin/betterdesk-server
UNIT
}

test_unit_environment() (
    load
    write_node_era_units
    local got
    got=$(unit_environment "$SERVER_UNIT" | tr '\n' ' ')
    [ "$got" = "AUTH_DB_PATH=/opt/BetterDeskConsole/data/auth.db MESH_ENABLED=Y SIGNAL_PORT=21116 GO_API_PORT=21114 CONSOLE_PORT=0 P2P_FIRST=Y P2P_FALLBACK_MS=2000 SAME_NAT_RELAY=Y " ] \
        || fail "unit_environment: $got"
    [ "$(unit_env_value "$LEGACY_CONSOLE_UNIT" PORT)" = 8080 ] || fail "unit_env_value PORT"
    [ -z "$(unit_env_value "$LEGACY_CONSOLE_UNIT" MISSING)" ] || fail "unit_env_value of a missing key"
    rm -rf "$TEST_DIR"
)

test_exec_args() (
    load
    write_node_era_units
    local args
    args=$(unit_exec_args "$SERVER_UNIT")
    [ "$args" = "-mode all -relay-servers 203.0.113.7 -key-file /opt/betterdesk/id_ed25519 -api-port 21114 -signal-rate-limit-per-ip 20 -tls-cert /opt/betterdesk/ssl/betterdesk.crt -tls-key /opt/betterdesk/ssl/betterdesk.key -tls-signal -tls-relay" ] \
        || fail "unit_exec_args: $args"
    [ "$(unit_exec_flag_value "$SERVER_UNIT" -db)" = 'postgres://u:p$w%d@localhost:5432/bd' ] || fail "-db value"
    [ "$(unit_exec_flag_value "$SERVER_UNIT" -init-admin-pass)" = 'S3cr$t' ] || fail "-init-admin-pass value"
    [ "$(unit_exec_binary "$SERVER_UNIT")" = /opt/betterdesk/betterdesk-server ] || fail "unit_exec_binary"
    rm -rf "$TEST_DIR"
)

# Under the old units .env won over Environment=; only the units' other
# settings move into .env, without what only Node read and without the
# CONSOLE_PORT=0 the installer added.
test_carry_unit_environment() (
    load
    write_node_era_units
    local env_file="$CONSOLE_PATH/.env"
    printf 'PORT=5000\nHOST=127.0.0.1\nSIGNAL_PORT=\n' > "$env_file"
    carry_unit_environment "$LEGACY_CONSOLE_UNIT" "$env_file"
    carry_unit_environment "$SERVER_UNIT" "$env_file"
    grep -qx 'PORT=5000' "$env_file" || fail "PORT of .env was replaced"
    [ "$(grep -c '^PORT=' "$env_file")" = 1 ] || fail "PORT carried although .env has it"
    grep -qx 'HOST=127.0.0.1' "$env_file" || fail "HOST of .env was replaced"
    grep -qx 'NODE_ENV=production' "$env_file" || fail "NODE_ENV not carried"
    grep -qx 'P2P_FIRST=Y' "$env_file" || fail "P2P_FIRST not carried"
    grep -qx 'SAME_NAT_RELAY=Y' "$env_file" || fail "SAME_NAT_RELAY not carried"
    [ "$(grep -c '^SIGNAL_PORT=' "$env_file")" = 1 ] || fail "SIGNAL_PORT carried although .env sets it (empty)"
    if grep -q '^CONSOLE_PORT=\|^NODE_EXTRA_CA_CERTS=\|^BETTERDESK_SERVER_BINARY=' "$env_file"; then
        fail "a skipped key was carried: $(grep '^CONSOLE_PORT=\|^NODE_EXTRA_CA_CERTS=\|^BETTERDESK_SERVER_BINARY=' "$env_file")"
    fi
    rm -rf "$TEST_DIR"
)

test_write_server_unit() (
    load
    write_server_unit "-mode all -api-port 21114" ""
    local unit="$SERVER_UNIT"
    grep -qx "User=betterdesk" "$unit" || fail "unit user"
    grep -qx "Environment=BETTERDESK_SUPERVISOR=systemd" "$unit" || fail "BETTERDESK_SUPERVISOR"
    grep -qx "Environment=BETTERDESK_ENV_FILE=$CONSOLE_PATH/.env" "$unit" || fail "BETTERDESK_ENV_FILE"
    grep -qx "SuccessExitStatus=3" "$unit" || fail "SuccessExitStatus"
    grep -qx "Restart=always" "$unit" || fail "Restart"
    grep -qx "ExecStart=$RUSTDESK_PATH/betterdesk-server -mode all -api-port 21114" "$unit" || fail "ExecStart"
    if grep -q 'CONSOLE_PORT\|EnvironmentFile\|__EMPTY__\|AmbientCapabilities' "$unit"; then
        fail "unexpected line in the unit: $(grep 'CONSOLE_PORT\|EnvironmentFile\|__EMPTY__\|AmbientCapabilities' "$unit")"
    fi
    is_go_only_install || fail "is_go_only_install on the written unit"

    # A console port below 1024 needs the capability; extra settings stay
    printf 'CONSOLE_PORT=80\n' > "$CONSOLE_PATH/.env"
    write_server_unit "-mode all" "Environment=TRUST_PROXY=Y"
    grep -qx "AmbientCapabilities=CAP_NET_BIND_SERVICE" "$unit" || fail "AmbientCapabilities for port 80"
    grep -qx "Environment=TRUST_PROXY=Y" "$unit" || fail "extra environment"
    [ "$(server_unit_extra_environment)" = "Environment=TRUST_PROXY=Y" ] || fail "server_unit_extra_environment: $(server_unit_extra_environment)"
    rm -rf "$TEST_DIR"
)

test_node_era_detection() (
    load
    write_node_era_units
    if is_go_only_install; then fail "a Node-era unit counts as the server with its console"; fi
    is_node_era_install || fail "is_node_era_install"
    rm -rf "$TEST_DIR"
)

# Stopping stale servers ends a running betterdesk-server, never a process
# that only names the binary in its arguments (the installer run with
# --binary /dist/betterdesk-server-linux-amd64).
test_kill_stale_processes() (
    load
    print_warning() { :; }
    mkdir -p "$TEST_DIR/bin"
    cp "$(command -v sleep)" "$TEST_DIR/bin/betterdesk-server"
    "$TEST_DIR/bin/betterdesk-server" 60 &
    local server=$!
    bash -c 'sleep 60; : --binary /dist/betterdesk-server-linux-amd64' &
    local installer=$!
    sleep 0.5
    kill_stale_processes "betterdesk-server"
    if kill -0 "$server" 2>/dev/null; then
        kill "$server" "$installer" 2>/dev/null || true
        fail "the stale betterdesk-server kept running"
    fi
    if ! kill -0 "$installer" 2>/dev/null; then
        fail "a process that only names betterdesk-server in its arguments was killed"
    fi
    kill "$installer" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$TEST_DIR"
)

test_unit_environment
test_exec_args
test_carry_unit_environment
test_write_server_unit
test_node_era_detection
test_kill_stale_processes
echo "Go installer unit checks passed"
