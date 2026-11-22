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

# fake_current_binary: an installed binary that checks release signatures
# itself (it names verify-release in its help), as this release's does.
fake_current_binary() {
    printf '#!/bin/sh\n# verify-release --manifest FILE (the commands it has)\nexit 0\n' > "$RUSTDESK_PATH/betterdesk-server"
    chmod +x "$RUSTDESK_PATH/betterdesk-server"
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
    if grep -q 'ENROLLMENT_MODE' "$unit"; then fail "the unit sets the enrollment mode"; fi
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

# L68: the signal and relay ports live in .env, where the admin changes
# them; the unit does not set them, and an update moves the ports an older
# unit set into .env instead of setting 21116 and 21117 again.
test_update_keeps_the_admins_ports() (
    load
    systemctl() { :; }
    print_info() { :; }
    write_server_unit "-mode all -api-port 21114" ""
    if grep -q 'SIGNAL_PORT\|RELAY_PORT' "$SERVER_UNIT"; then fail "the unit sets $(grep 'SIGNAL_PORT\|RELAY_PORT' "$SERVER_UNIT")"; fi
    sed -i '/^Environment=BETTERDESK_SUPERVISOR=/a Environment=SIGNAL_PORT=31116\nEnvironment=RELAY_PORT=31117' "$SERVER_UNIT"
    printf 'SIGNAL_PORT=21116\nRELAY_PORT=21117\nSESSION_SECRET=kept\n' > "$CONSOLE_PATH/.env"
    maybe_update_services
    if grep -q 'SIGNAL_PORT\|RELAY_PORT' "$SERVER_UNIT"; then fail "the updated unit sets $(grep 'SIGNAL_PORT\|RELAY_PORT' "$SERVER_UNIT")"; fi
    grep -qx 'SIGNAL_PORT=31116' "$CONSOLE_PATH/.env" && grep -qx 'RELAY_PORT=31117' "$CONSOLE_PATH/.env" \
        || fail "the update lost the unit's ports: $(grep '_PORT=' "$CONSOLE_PATH/.env" | tr '\n' ' ')"
    grep -qx 'SESSION_SECRET=kept' "$CONSOLE_PATH/.env" || fail "the update lost a line of .env"

    # A repair puts the ports into .env when it has none, and leaves the
    # admin's alone
    ensure_go_server_signal_ports || true
    grep -qx 'SIGNAL_PORT=31116' "$CONSOLE_PATH/.env" || fail "a repair changed the admin's signal port"
    printf 'SESSION_SECRET=kept\n' > "$CONSOLE_PATH/.env"
    ensure_go_server_signal_ports || true
    grep -qx 'SIGNAL_PORT=21116' "$CONSOLE_PATH/.env" && grep -qx 'RELAY_PORT=21117' "$CONSOLE_PATH/.env" \
        || fail "a repair left .env without ports: $(cat "$CONSOLE_PATH/.env")"
    if grep -q 'SIGNAL_PORT\|RELAY_PORT' "$SERVER_UNIT"; then fail "a repair pinned $(grep 'SIGNAL_PORT\|RELAY_PORT' "$SERVER_UNIT")"; fi
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

# A fresh .env gives the server the admin password the console shows
# (INIT_ADMIN_PASS): the server makes the first admin before the console
# starts, and the console keeps an admin it did not make.
test_fresh_env_admin_password() (
    load
    cat > "$RUSTDESK_PATH/betterdesk-server" <<'FAKE'
#!/bin/bash
# Stands in for the binary: upgrade-env --from FILE (the commands it has)
if [ "$1 $2" = "env merge" ]; then
    printf 'DEFAULT_ADMIN_PASSWORD=%s\nDB_TYPE=sqlite\n' "$BD_SUBST_DEFAULT_ADMIN_PASSWORD" > "$4"
    echo '{"success":true,"mode":"fresh","added":[]}'
fi
exit 0
FAKE
    chmod +x "$RUSTDESK_PATH/betterdesk-server"
    ADMIN_PASSWORD='Fresh-Admin-Pass-1'
    USE_POSTGRESQL=false
    merge_console_env true >/dev/null
    grep -qx 'INIT_ADMIN_PASS=Fresh-Admin-Pass-1' "$CONSOLE_PATH/.env" \
        || fail "a fresh .env has no INIT_ADMIN_PASS of the admin password: $(grep -c INIT_ADMIN_PASS "$CONSOLE_PATH/.env") lines"
    # The binary stands in for an older one, which reads DB_URL
    grep -qx "DB_URL=$RUSTDESK_PATH/db_v2.sqlite3" "$CONSOLE_PATH/.env" || fail "DB_URL of a fresh SQLite install on an older binary"
    # L65: a fresh install starts in Managed; .env is only the first
    # default, a mode saved in the console wins
    grep -qx 'ENROLLMENT_MODE=managed' "$CONSOLE_PATH/.env" || fail "a fresh .env does not start in Managed"
    rm -rf "$TEST_DIR"
)

# database_lines FILE: the lines of an env file that name the database.
database_lines() {
    grep -E '^(DATABASE_URL|DB_URL|DB_TYPE|DB_PATH)=' "$1" | tr '\n' ' '
}

# L70: the installer writes the database once, as DATABASE_URL, for a
# binary that reads it; older env files are read as the server reads them.
test_database_url_only() (
    load
    cat > "$RUSTDESK_PATH/betterdesk-server" <<'FAKE'
#!/bin/bash
# Stands in for the binary: upgrade-env --from FILE (the commands it has),
# and the server's warning "Remove DB_TYPE to use DATABASE_URL". env merge
# writes the template's lines, or appends those the file lacks
if [ "$1 $2" = "env merge" ]; then
    case " $* " in *" --fresh "*) : > "$4" ;; esac
    grep -q '^DEFAULT_ADMIN_PASSWORD=' "$4" 2>/dev/null || printf 'DEFAULT_ADMIN_PASSWORD=%s\n' "$BD_SUBST_DEFAULT_ADMIN_PASSWORD" >> "$4"
    grep -q '^DATABASE_URL=' "$4" 2>/dev/null || printf 'DATABASE_URL=%s\n' "$BD_SUBST_DATABASE_URL" >> "$4"
    echo '{"success":true,"mode":"merge","added":[]}'
fi
exit 0
FAKE
    chmod +x "$RUSTDESK_PATH/betterdesk-server"
    ADMIN_PASSWORD='Database-Url-Pass-1'
    USE_POSTGRESQL=false
    merge_console_env true >/dev/null
    [ "$(database_lines "$CONSOLE_PATH/.env")" = "DATABASE_URL=$RUSTDESK_PATH/db_v2.sqlite3 " ] \
        || fail "a fresh SQLite install names its database as: $(database_lines "$CONSOLE_PATH/.env")"
    local pg='postgres://bd:p&w|d@127.0.0.1:5432/bd?sslmode=disable'
    rm -f "$CONSOLE_PATH/.env"
    USE_POSTGRESQL=true
    POSTGRESQL_URI="$pg"
    merge_console_env true >/dev/null
    [ "$(database_lines "$CONSOLE_PATH/.env")" = "DATABASE_URL=$pg " ] \
        || fail "a fresh PostgreSQL install names its database as: $(database_lines "$CONSOLE_PATH/.env")"

    # A move between databases leaves DATABASE_URL alone, also in an older
    # file (one without its last line end)
    printf 'SESSION_SECRET=kept\nDB_TYPE=sqlite\nDB_PATH=/old/db.sqlite3\nDATABASE_URL=\nDB_URL=/old/db.sqlite3' > "$CONSOLE_PATH/.env"
    use_database_in_env postgres "$pg"
    [ "$(database_lines "$CONSOLE_PATH/.env")" = "DATABASE_URL=$pg " ] \
        || fail "the move to PostgreSQL names the database as: $(database_lines "$CONSOLE_PATH/.env")"
    grep -qx 'SESSION_SECRET=kept' "$CONSOLE_PATH/.env" || fail "the move lost another line of .env"
    use_database_in_env sqlite /data/db_v2.sqlite3
    [ "$(database_lines "$CONSOLE_PATH/.env")" = "DATABASE_URL=/data/db_v2.sqlite3 " ] \
        || fail "the move back to SQLite names the database as: $(database_lines "$CONSOLE_PATH/.env")"

    # An update keeps an older file as it is, and preserves its database
    printf 'DB_TYPE=postgres\nDATABASE_URL=%s\nDB_URL=%s\n' "$pg" "$pg" > "$CONSOLE_PATH/.env"
    USE_POSTGRESQL=false
    POSTGRESQL_URI=""
    preserve_database_config
    [ "$USE_POSTGRESQL" = true ] && [ "$POSTGRESQL_URI" = "$pg" ] || fail "an older PostgreSQL install is not seen as one"
    merge_console_env false >/dev/null
    [ "$(database_lines "$CONSOLE_PATH/.env")" = "DB_TYPE=postgres DATABASE_URL=$pg DB_URL=$pg " ] \
        || fail "an update changed an older file's database: $(database_lines "$CONSOLE_PATH/.env")"
    # A file of this installer names PostgreSQL by its address alone
    printf 'DATABASE_URL=%s\n' "$pg" > "$CONSOLE_PATH/.env"
    USE_POSTGRESQL=false
    preserve_database_config
    [ "$USE_POSTGRESQL" = true ] && [ "$POSTGRESQL_URI" = "$pg" ] || fail "DATABASE_URL alone is not seen as PostgreSQL"
    printf 'SESSION_SECRET=kept\n' > "$CONSOLE_PATH/.env"
    USE_POSTGRESQL=false
    merge_console_env false >/dev/null
    [ "$(database_lines "$CONSOLE_PATH/.env")" = "DATABASE_URL=$RUSTDESK_PATH/db_v2.sqlite3 " ] \
        || fail "an update of a file that names no database writes: $(database_lines "$CONSOLE_PATH/.env")"

    # Read as the server reads them (envvar.DatabaseFrom)
    local file="$TEST_DIR/read.env" want text
    while IFS='|' read -r want text; do
        printf '%b' "$text" > "$file"
        [ "$(env_database_type "$file") $(env_database_url "$file")" = "$want" ] \
            || fail "$text is read as '$(env_database_type "$file") $(env_database_url "$file")', want '$want'"
    done <<'CASES'
postgres postgres://a@h/d|DB_TYPE=postgres\nDATABASE_URL=postgres://a@h/d\nDB_URL=postgres://a@h/d\n
sqlite /old/db|DB_TYPE=sqlite\nDATABASE_URL=\nDB_URL=/old/db\n
sqlite /old/db|DB_TYPE=sqlite\nDATABASE_URL=postgres://a@h/d\nDB_URL=/old/db\n
sqlite |DB_TYPE=SQLite\nDATABASE_URL=postgres://a@h/d\nDB_URL=postgresql://a@h/d\n
postgres postgresql://a@h/d|DATABASE_URL=postgresql://a@h/d\n
postgres postgres://a@h/d|DATABASE_URL=\nDB_URL=postgres://a@h/d\n
sqlite /srv/db.sqlite3|DATABASE_URL=sqlite:///srv/db.sqlite3\nDB_URL=/other\n
sqlite /srv/db.sqlite3|DATABASE_URL=SQLite:/srv/db.sqlite3\n
sqlite |SESSION_SECRET=x\n
CASES
    rm -rf "$TEST_DIR"
)

# UP8: an update channel given to the installer (install.sh passes the
# branch it installed) goes into .env over the template's main, on a fresh
# install and on an update; without one .env keeps what it has.
test_update_channel_in_env() (
    export UPDATE_GITHUB_BRANCH=dev
    load
    cat > "$RUSTDESK_PATH/betterdesk-server" <<'FAKE'
#!/bin/bash
# Stands in for the binary: upgrade-env --from FILE (the commands it has)
if [ "$1 $2" = "env merge" ]; then
    grep -q '^UPDATE_GITHUB_BRANCH=' "$4" 2>/dev/null || printf 'UPDATE_GITHUB_BRANCH=main\n' >> "$4"
    echo '{"success":true,"mode":"merge","added":[]}'
fi
exit 0
FAKE
    chmod +x "$RUSTDESK_PATH/betterdesk-server"
    ADMIN_PASSWORD='Channel-Test-1'
    USE_POSTGRESQL=false
    merge_console_env true >/dev/null
    grep -qx 'UPDATE_GITHUB_BRANCH=dev' "$CONSOLE_PATH/.env" \
        || fail "a fresh install from dev writes $(grep '^UPDATE_GITHUB_BRANCH=' "$CONSOLE_PATH/.env")"

    # An update reads the channel before it downloads the binary
    printf 'UPDATE_GITHUB_BRANCH=main\n' > "$CONSOLE_PATH/.env"
    read_update_github_branch_from_env
    [ "$UPDATE_GITHUB_BRANCH" = dev ] || fail "the given channel lost to the main in .env: $UPDATE_GITHUB_BRANCH"
    merge_console_env false >/dev/null
    [ "$(grep -c '^UPDATE_GITHUB_BRANCH=' "$CONSOLE_PATH/.env")" = 1 ] || fail "the key is in .env twice"
    grep -qx 'UPDATE_GITHUB_BRANCH=dev' "$CONSOLE_PATH/.env" \
        || fail "an update with a given channel keeps $(grep '^UPDATE_GITHUB_BRANCH=' "$CONSOLE_PATH/.env")"
    rm -rf "$TEST_DIR"
)

# The server's own updater reads the channel from .env, so a given channel
# is in .env before betterdesk-server update runs
test_update_channel_before_server_update() (
    export UPDATE_GITHUB_BRANCH=dev
    load
    printf 'UPDATE_GITHUB_BRANCH=main\n' > "$CONSOLE_PATH/.env"
    do_backup_silent() { :; }
    print_step() { :; }
    print_error() { :; }
    SERVER_BINARY_FILE=""
    FROM_SOURCE=false
    unset RELEASE_BASE_URL
    fake_current_binary
    server_cmd() {
        grep '^UPDATE_GITHUB_BRANCH=' "$CONSOLE_PATH/.env" > "$TEST_DIR/seen"
        return 1
    }
    update_go_install >/dev/null 2>&1 || true
    [ "$(cat "$TEST_DIR/seen" 2>/dev/null)" = "UPDATE_GITHUB_BRANCH=dev" ] \
        || fail "betterdesk-server update ran with $(cat "$TEST_DIR/seen" 2>/dev/null || echo 'no channel') in .env"
    rm -rf "$TEST_DIR"
)

test_update_channel_kept_without_one() (
    unset UPDATE_GITHUB_BRANCH
    load
    cat > "$RUSTDESK_PATH/betterdesk-server" <<'FAKE'
#!/bin/bash
# Stands in for the binary: upgrade-env --from FILE (the commands it has)
[ "$1 $2" = "env merge" ] && echo '{"success":true,"mode":"merge","added":[]}'
exit 0
FAKE
    chmod +x "$RUSTDESK_PATH/betterdesk-server"
    USE_POSTGRESQL=false
    printf 'UPDATE_GITHUB_BRANCH=dev\nDEFAULT_ADMIN_PASSWORD=x\n' > "$CONSOLE_PATH/.env"
    merge_console_env false >/dev/null
    grep -qx 'UPDATE_GITHUB_BRANCH=dev' "$CONSOLE_PATH/.env" || fail "an update without a channel changed .env"
    if grep -q '^ENROLLMENT_MODE' "$CONSOLE_PATH/.env"; then fail "an update added an enrollment mode to .env"; fi
    read_update_github_branch_from_env
    [ "$UPDATE_GITHUB_BRANCH" = dev ] || fail "the channel of .env was not read: $UPDATE_GITHUB_BRANCH"
    rm -rf "$TEST_DIR"
)

# The server folder holds the database and the keys: the service's group
# may write it, nobody else may list or read it (upstream audit C3).
test_server_folder_mode() (
    load
    SERVICE_USER=$(id -un)
    printf 'x' > "$RUSTDESK_PATH/db_v2.sqlite3"
    set_server_folder_permissions
    mode=$(stat -c %a "$RUSTDESK_PATH")
    [ "$mode" = 2770 ] || fail "the server folder has mode $mode, want 2770"
    rm -rf "$TEST_DIR"
)

# The console's language files, themes and images come from the binary:
# an update copies none from a web-nodejs folder an older release left in
# the checkout, and the console folder keeps its .env, data, installed
# fonts and language files.
test_console_home_copies_no_console_files() (
    load
    SCRIPT_DIR="$TEST_DIR/checkout"
    mkdir -p "$SCRIPT_DIR/web-nodejs/lang" "$SCRIPT_DIR/web-nodejs/themes"
    printf '{"old":true}\n' > "$SCRIPT_DIR/web-nodejs/lang/en.json"
    printf 'old\n' > "$SCRIPT_DIR/web-nodejs/themes/dark.css"
    mkdir -p "$CONSOLE_PATH/data/transfers" "$CONSOLE_PATH/public/fonts" "$CONSOLE_PATH/lang"
    printf 'SESSION_SECRET=kept\n' > "$CONSOLE_PATH/.env"
    printf 'file\n' > "$CONSOLE_PATH/data/transfers/a.bin"
    printf 'font\n' > "$CONSOLE_PATH/public/fonts/inter.woff2"
    printf '{"custom":true}\n' > "$CONSOLE_PATH/lang/xx.json"
    SERVICE_USER=$(id -un)
    MINIMAL_MODE=false
    STORE_ADMIN_CREDENTIALS=false
    print_step() { :; }
    print_success() { :; }
    ensure_service_user() { :; }
    merge_console_env() { [ "$1" = false ] || fail "an install with .env counted as fresh"; }
    setup_console_home >/dev/null
    [ ! -e "$CONSOLE_PATH/lang/en.json" ] || fail "the update copied the checkout's web-nodejs/lang into the console folder"
    [ ! -e "$CONSOLE_PATH/themes" ] || fail "the update copied the checkout's web-nodejs/themes into the console folder"
    [ "$(cat "$CONSOLE_PATH/.env")" = SESSION_SECRET=kept ] || fail "the update changed .env"
    [ -f "$CONSOLE_PATH/data/transfers/a.bin" ] || fail "the update lost a file of data/"
    [ -f "$CONSOLE_PATH/public/fonts/inter.woff2" ] || fail "the update lost an installed font"
    [ -f "$CONSOLE_PATH/lang/xx.json" ] || fail "the update lost a language file of the console folder"
    rm -rf "$TEST_DIR"
)

# L31: releases, the update channel and the unit's documentation link
# name the fork's repository.
test_releases_come_from_the_fork() (
    unset UPDATE_GITHUB_OWNER UPDATE_GITHUB_REPO UPDATE_GITHUB_BRANCH RELEASE_BASE_URL
    load
    local base
    base=$(release_base_url)
    [ "$base" = "https://github.com/trybetterdesk/betterdesk/releases/latest/download" ] \
        || fail "the stable channel downloads from $base"
    UPDATE_GITHUB_BRANCH=dev
    base=$(release_base_url)
    [ "$base" = "https://github.com/trybetterdesk/betterdesk/releases/download/dev" ] \
        || fail "the development channel downloads from $base"
    write_server_unit "-mode all" ""
    grep -qx "Documentation=https://github.com/trybetterdesk/betterdesk" "$SERVER_UNIT" \
        || fail "the unit's documentation link: $(grep '^Documentation=' "$SERVER_UNIT")"
    rm -rf "$TEST_DIR"
)

# L140: --channel picks the update channel, and a failed download says
# what to do: no stable release yet, or no way out to the internet.
test_channel_and_download_errors() (
    unset UPDATE_GITHUB_BRANCH RELEASE_BASE_URL UPDATE_GITHUB_OWNER UPDATE_GITHUB_REPO
    set -- --channel dev
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/betterdesk.sh"
    [ "$UPDATE_GITHUB_BRANCH" = dev ] && [ "$UPDATE_GITHUB_BRANCH_GIVEN" = dev ] \
        || fail "--channel dev gives the channel '$UPDATE_GITHUB_BRANCH' (given '$UPDATE_GITHUB_BRANCH_GIVEN')"
    load
    print_info() { echo "$1"; }
    unset UPDATE_GITHUB_BRANCH_GIVEN
    UPDATE_GITHUB_BRANCH=main
    local out
    curl() { printf '404'; }
    out=$(download_release_binary "$TEST_DIR" 2>&1) && fail "a download that found nothing succeeded"
    case "$out" in
        *"no stable release"*"--channel dev"*"--binary"*) ;;
        *) fail "a missing stable release says: $out" ;;
    esac
    curl() { printf '000'; return 6; }
    out=$(download_release_binary "$TEST_DIR" 2>&1) && fail "a download without network succeeded"
    case "$out" in
        *"Could not reach"*"--binary"*) ;;
        *) fail "a download without network says: $out" ;;
    esac
    if bash "$SCRIPT_ROOT/betterdesk.sh" --channel nightly >/dev/null 2>&1; then fail "--channel nightly was taken"; fi
    rm -rf "$TEST_DIR"
)

# L141: the installer runs piped from curl, takes no answers from a pipe,
# and leaves the betterdesk command behind for updates and the uninstall.
test_one_command_install() (
    local out
    # main runs: it asks for root, or as root it looks for an install
    out=$(cd "$(mktemp -d)" && timeout 60 bash -s -- --check-permissions < "$SCRIPT_ROOT/betterdesk.sh" 2>&1 || true)
    case "$out" in
        *"requires root"*|*"Detecting installation"*) ;;
        *) fail "betterdesk.sh piped into bash did not start: $out" ;;
    esac
    load
    AUTO_MODE=false
    default_to_auto_without_terminal < /dev/null
    [ "$AUTO_MODE" = true ] || fail "without a terminal the installer would ask questions"
    out=$( (press_enter; confirm "Go on?" && echo yes || echo no) < /dev/null 2>/dev/null)
    case "$out" in
        *no) ;;
        *) fail "without a terminal a question did not take its default: $out" ;;
    esac
    MANAGER_COMMAND="$TEST_DIR/sbin/betterdesk"
    install_manager_command >/dev/null
    [ -x "$MANAGER_COMMAND" ] || fail "no betterdesk command was left behind"
    cmp -s "$MANAGER_COMMAND" "$SCRIPT_ROOT/betterdesk.sh" || fail "the betterdesk command is not this installer"
    # Piped from curl, it fetches itself from the repository's default
    # branch, whatever the release channel: main holds upstream's older
    # installer
    rm -f "$MANAGER_COMMAND"
    BETTERDESK_SELF=""
    UPDATE_GITHUB_BRANCH=main
    curl() {
        local a prev="" dest=""
        for a in "$@"; do [ "$prev" = -o ] && dest="$a"; prev="$a"; done
        printf '%s\n' "${!#}" > "$TEST_DIR/fetched"
        cp "$SCRIPT_ROOT/betterdesk.sh" "$dest"
    }
    install_manager_command >/dev/null
    [ "$(cat "$TEST_DIR/fetched")" = "https://raw.githubusercontent.com/trybetterdesk/betterdesk/dev/betterdesk.sh" ] \
        || fail "the betterdesk command came from $(cat "$TEST_DIR/fetched")"
    unset -f curl
    rm -rf "$TEST_DIR"
)

# L201: a run without a terminal or TERM (cloud-init, CI, ssh host cmd)
# gets past the header instead of stopping at clear under set -e.
test_runs_without_term() (
    load
    local out
    # A shell of its own: set -e is in force there as in a run of the script
    out=$(env -u TERM bash -c 'script=$1; set --; source "$script"; print_header; echo "after the header"' \
        test "$SCRIPT_ROOT/betterdesk.sh" 2>&1 < /dev/null) || true
    case "$out" in *"after the header"*) ;; *) fail "the header stopped a run without TERM: $out" ;; esac
    rm -rf "$TEST_DIR"
)

# L207: a package source that fails in apt-get update (a third-party
# repository, a mirror in sync) does not stop the install: the packages are
# installed from the lists there are, and the run says so.
test_apt_source_failure_goes_on() (
    local out
    out=$(bash -c 'script=$1; set --; source "$script"
        LOG_FILE=/dev/null
        apt-get() { if [ "$1" = update ]; then echo "E: Failed to fetch https://packages.example.org/Packages.xz" >&2; return 100; fi; echo "apt-get $*"; }
        install_dependencies; echo "after the dependencies"' test "$SCRIPT_ROOT/betterdesk.sh" 2>&1 < /dev/null) || true
    case "$out" in *"apt-get install -y -qq sqlite3"*"after the dependencies"*) ;; *) fail "a failing package source stopped the install: $out" ;; esac
    case "$out" in *"apt-get update failed for a package source"*) ;; *) fail "the failing package source is not said: $out" ;; esac
)

# A fresh install from the one-liner without --channel takes the stable
# channel, and while the repository has no stable release it installs the
# development build, says so and keeps that channel. A channel given
# (--channel stable) or saved in .env gets no fallback but the error.
test_no_stable_release_installs_dev() (
    unset UPDATE_GITHUB_OWNER UPDATE_GITHUB_REPO RELEASE_BASE_URL
    load
    print_info() { echo "$1"; }
    print_warning() { echo "$1"; }
    print_error() { echo "$1"; }
    release_asset_name() { echo betterdesk-server-linux-amd64; }
    unset UPDATE_GITHUB_BRANCH_GIVEN
    UPDATE_GITHUB_BRANCH=main
    SKIP_VERIFY=true
    # curl: no stable release, a development build
    curl() {
        local a prev="" dest="" url="${!#}"
        for a in "$@"; do [ "$prev" = -o ] && dest="$a"; prev="$a"; done
        case "$url" in
            */releases/latest/download/*) printf '404' ;;
            */releases/download/dev/SHA256SUMS)
                printf '%s  betterdesk-server-linux-amd64\n' "$(printf 'dev build\n' | sha256sum | cut -d' ' -f1)" > "$dest"
                printf '200' ;;
            */releases/download/dev/betterdesk-server-linux-amd64) printf 'dev build\n' > "$dest"; printf '200' ;;
            *) printf '404' ;;
        esac
    }
    download_release_binary "$TEST_DIR" > "$TEST_DIR/out" 2>&1 || fail "no stable release and no fallback: $(cat "$TEST_DIR/out")"
    [ -f "$TEST_DIR/betterdesk-server" ] || fail "the development build is not staged"
    grep -q 'no stable release yet: installing the development build' "$TEST_DIR/out" || fail "the fallback is not said: $(cat "$TEST_DIR/out")"
    grep -q -- '--update --channel stable' "$TEST_DIR/out" || fail "the fallback does not say how to go back to stable: $(cat "$TEST_DIR/out")"
    [ "$UPDATE_GITHUB_BRANCH" = dev ] && [ "$UPDATE_GITHUB_BRANCH_GIVEN" = dev ] \
        || fail "the fallback does not keep the development channel ($UPDATE_GITHUB_BRANCH, given ${UPDATE_GITHUB_BRANCH_GIVEN:-none})"
    printf 'SESSION_SECRET=kept\n' > "$CONSOLE_PATH/.env"
    save_given_update_channel
    grep -qx 'UPDATE_GITHUB_BRANCH=dev' "$CONSOLE_PATH/.env" || fail "the development channel does not reach .env"

    local out
    rm -f "$TEST_DIR/betterdesk-server"
    UPDATE_GITHUB_BRANCH=main
    UPDATE_GITHUB_BRANCH_GIVEN=main
    out=$(download_release_binary "$TEST_DIR" 2>&1) && fail "--channel stable installed the development build: $out"
    case "$out" in *"no stable release yet"*"--channel dev"*) ;; *) fail "--channel stable without a release says: $out" ;; esac
    case "$out" in *"installing the development build"*) fail "--channel stable fell back: $out" ;; esac
    unset UPDATE_GITHUB_BRANCH_GIVEN
    printf 'UPDATE_GITHUB_BRANCH=main\n' > "$CONSOLE_PATH/.env"
    out=$(download_release_binary "$TEST_DIR" 2>&1) && fail "the channel saved in .env fell back: $out"
    rm -rf "$TEST_DIR"
)

# L143: a second run updates instead of installing again, the password
# shows only when the install made it, and after the first start .env and
# its copies keep no admin password.
test_rerun_updates_and_forgets_the_password() (
    load
    [ "$(auto_action)" = install ] || fail "a machine without BetterDesk gets $(auto_action)"
    write_server_unit "-mode all" ""
    printf '#!/bin/sh\n' > "$RUSTDESK_PATH/betterdesk-server"
    chmod +x "$RUSTDESK_PATH/betterdesk-server"
    printf 'DEFAULT_ADMIN_PASSWORD=First-Pass-1\nINIT_ADMIN_PASS=First-Pass-1\nSESSION_SECRET=kept\n' > "$CONSOLE_PATH/.env"
    cp "$CONSOLE_PATH/.env" "$CONSOLE_PATH/.env.pre-go"
    [ "$(auto_action)" = update ] || fail "a second run on an install gets $(auto_action)"
    USE_POSTGRESQL=true
    [ "$(auto_action)" = install ] || fail "--postgresql on a SQLite install gets $(auto_action), not the move"
    USE_POSTGRESQL=false
    UNINSTALL_MODE=true
    [ "$(auto_action)" = uninstall ] || fail "--uninstall on an install gets $(auto_action)"
    UNINSTALL_MODE=false

    local out
    FRESH_CONSOLE_ENV=false
    ADMIN_PASSWORD=""
    out=$(create_admin_user 2>&1)
    case "$out" in *First-Pass-1*) fail "a run that kept .env showed the admin password again" ;; esac
    FRESH_CONSOLE_ENV=true
    out=$(create_admin_user 2>&1)
    case "$out" in *First-Pass-1*) ;; *) fail "a fresh install did not show the admin password" ;; esac

    forget_first_admin_password
    for f in "$CONSOLE_PATH/.env" "$CONSOLE_PATH/.env.pre-go"; do
        if grep -q 'First-Pass-1' "$f"; then fail "$(basename "$f") keeps the admin password after the first start"; fi
    done
    grep -qx 'DEFAULT_ADMIN_PASSWORD=' "$CONSOLE_PATH/.env" || fail "DEFAULT_ADMIN_PASSWORD is not left empty"
    grep -qx 'SESSION_SECRET=kept' "$CONSOLE_PATH/.env" || fail "forgetting the password lost a line of .env"
    rm -rf "$TEST_DIR"
)

# L69: the host clients get becomes PANEL_PUBLIC_HOST, so the HTTP port
# redirects to HTTPS there; a value the admin set stays.
test_panel_public_host() (
    load
    systemctl() { :; }
    printf 'SESSION_SECRET=kept\n' > "$CONSOLE_PATH/.env"
    set_panel_public_host "203.0.113.7:21117"
    grep -qx 'PANEL_PUBLIC_HOST=203.0.113.7' "$CONSOLE_PATH/.env" || fail "PANEL_PUBLIC_HOST from host:port: $(grep PANEL_PUBLIC_HOST "$CONSOLE_PATH/.env")"
    set_panel_public_host "198.51.100.1"
    grep -qx 'PANEL_PUBLIC_HOST=203.0.113.7' "$CONSOLE_PATH/.env" || fail "a second address replaced PANEL_PUBLIC_HOST"
    printf 'PANEL_PUBLIC_HOST=\n' > "$CONSOLE_PATH/.env"
    write_server_unit "-mode all -relay-servers desk.example.org -api-port 21114" ""
    maybe_update_services >/dev/null
    grep -qx 'PANEL_PUBLIC_HOST=desk.example.org' "$CONSOLE_PATH/.env" || fail "an update set no PANEL_PUBLIC_HOST: $(cat "$CONSOLE_PATH/.env")"
    rm -rf "$TEST_DIR"
)

# L149: the end of an install names the console's address, the first
# sign-in, the client settings with the whole key, the log, the settings
# file and the commands for update and uninstall, and shows no password.
test_install_summary() (
    load
    printf 'PANEL_PUBLIC_HOST=desk.example.org\nHTTPS_ENABLED=true\nHTTPS_PORT=5443\nCONSOLE_PORT=5000\nDEFAULT_ADMIN_PASSWORD=\n' > "$CONSOLE_PATH/.env"
    printf 'AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJ=\n' > "$RUSTDESK_PATH/id_ed25519.pub"
    ADMIN_PASSWORD='Summary-Pass-1'
    local out
    out=$(print_install_summary 2>&1)
    # L76: clients get the client API port, plain HTTP while the
    # certificate is self-signed (RustDesk refuses it)
    printf 'API_PORT=21121\nRUSTDESK_API_TLS=false\n' >> "$CONSOLE_PATH/.env"
    out=$(print_install_summary 2>&1)
    case "$out" in *"API server:    "*"http://desk.example.org:21121"*) ;; *) fail "the summary gives clients another API address: $out" ;; esac
    for want in "https://desk.example.org:5443" "admin" "ID server" "desk.example.org" \
        "AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJ=" "journalctl -u betterdesk-server" "$CONSOLE_PATH/.env" \
        "betterdesk --update" "betterdesk --uninstall"; do
        case "$out" in *"$want"*) ;; *) fail "the summary does not name $want: $out" ;; esac
    done
    case "$out" in *Summary-Pass-1*) fail "the summary shows the password" ;; esac
    rm -rf "$TEST_DIR"
)

# L149: the uninstall says what it leaves on the machine, and --purge
# removes the data and the betterdesk command too.
test_uninstall_says_what_stays() (
    load
    systemctl() { :; }
    do_backup_silent() { :; }
    print_header() { :; }
    AUTO_MODE=true
    UNINSTALL_MODE=true
    MANAGER_COMMAND="$TEST_DIR/sbin/betterdesk"
    mkdir -p "$TEST_DIR/sbin" && printf '#!/bin/sh\n' > "$MANAGER_COMMAND" && chmod +x "$MANAGER_COMMAND"
    printf 'DB_TYPE=sqlite\n' > "$CONSOLE_PATH/.env"
    local out
    out=$(do_uninstall 2>&1)
    for want in "Left on this machine" "$RUSTDESK_PATH" "$CONSOLE_PATH" "--purge" "$MANAGER_COMMAND"; do
        case "$out" in *"$want"*) ;; *) fail "the uninstall does not name $want: $out" ;; esac
    done
    [ -d "$RUSTDESK_PATH" ] && [ -d "$CONSOLE_PATH" ] || fail "the uninstall without --purge removed the data"
    PURGE_MODE=true
    do_uninstall >/dev/null 2>&1
    [ ! -e "$RUSTDESK_PATH" ] && [ ! -e "$CONSOLE_PATH" ] && [ ! -e "$MANAGER_COMMAND" ] \
        || fail "--purge left $(ls -d "$RUSTDESK_PATH" "$CONSOLE_PATH" "$MANAGER_COMMAND" 2>/dev/null | tr '\n' ' ')"
    rm -rf "$TEST_DIR"
)

# L155: the server's updater is told the repository of the releases, so an
# installed binary with an older default does not ask upstream; one named
# in .env stays.
test_update_asks_the_fork() (
    unset UPDATE_GITHUB_OWNER UPDATE_GITHUB_REPO
    load
    do_backup_silent() { :; }
    print_step() { :; }
    print_error() { :; }
    SERVER_BINARY_FILE=""
    FROM_SOURCE=false
    unset RELEASE_BASE_URL
    fake_current_binary
    server_cmd() { printf '%s/%s\n' "${UPDATE_GITHUB_OWNER:-}" "${UPDATE_GITHUB_REPO:-}" > "$TEST_DIR/seen"; return 1; }
    printf 'SESSION_SECRET=kept\n' > "$CONSOLE_PATH/.env"
    update_go_install >/dev/null 2>&1 || true
    [ "$(cat "$TEST_DIR/seen" 2>/dev/null)" = trybetterdesk/betterdesk ] \
        || fail "betterdesk-server update ran for $(cat "$TEST_DIR/seen" 2>/dev/null || echo 'no repository')"
    printf 'UPDATE_GITHUB_OWNER=acme\nUPDATE_GITHUB_REPO=desk\n' > "$CONSOLE_PATH/.env"
    update_go_install >/dev/null 2>&1 || true
    [ "$(cat "$TEST_DIR/seen" 2>/dev/null)" = acme/desk ] || fail "the repository of .env lost to the default: $(cat "$TEST_DIR/seen")"
    rm -rf "$TEST_DIR"
)

# L67: both installers open the same ports: RustDesk's (21115 to 21117 TCP,
# 21116 UDP), the client API port, 21122 while CDAP is on, and the
# console's ports unless TLS ends at a reverse proxy.
test_firewall_ports() (
    load
    printf 'API_PORT=21121\nCONSOLE_PORT=5000\nHTTPS_ENABLED=true\nHTTPS_PORT=5443\nCDAP_ENABLED=Y\nTRUST_PROXY=false\n' > "$CONSOLE_PATH/.env"
    [ "$(firewall_ports | tr '\n' ' ')" = "21115/tcp 21116/tcp 21116/udp 21117/tcp 21121/tcp 21122/tcp 5000/tcp 5443/tcp " ] \
        || fail "the firewall ports: $(firewall_ports | tr '\n' ' ')"
    printf 'API_PORT=21121\nCONSOLE_PORT=5000\nHTTPS_ENABLED=false\nCDAP_ENABLED=N\nTRUST_PROXY=Y\n' > "$CONSOLE_PATH/.env"
    [ "$(firewall_ports | tr '\n' ' ')" = "21115/tcp 21116/tcp 21116/udp 21117/tcp 21121/tcp " ] \
        || fail "the firewall ports behind a reverse proxy without CDAP: $(firewall_ports | tr '\n' ' ')"
    rm -rf "$TEST_DIR"
)

# L76: a self-signed certificate keeps the client API port on HTTP, which
# RustDesk clients accept; the console gets HTTPS.
test_self_signed_keeps_the_client_api_on_http() (
    load
    command -v openssl >/dev/null || { echo "skipped: no openssl"; exit 0; }
    get_public_ip() { echo 203.0.113.7; }
    deploy_ssl_material_to_rustdesk_dir() { return 0; }
    print_success() { :; }
    printf 'HTTPS_ENABLED=false\nSSL_CERT_PATH=\nSSL_KEY_PATH=\nRUSTDESK_API_TLS=auto\n' > "$CONSOLE_PATH/.env"
    generate_ssl_certificates >/dev/null 2>&1 || fail "no certificate was made"
    grep -qx 'HTTPS_ENABLED=true' "$CONSOLE_PATH/.env" || fail "HTTPS is not on"
    grep -qx 'RUSTDESK_API_TLS=false' "$CONSOLE_PATH/.env" || fail "the client API port takes the self-signed certificate: $(grep RUSTDESK_API_TLS "$CONSOLE_PATH/.env")"

    # L66: a binary with the cert command makes the pair, no OpenSSL needed
    rm -rf "$RUSTDESK_PATH/ssl"
    cat > "$RUSTDESK_PATH/betterdesk-server" <<'FAKE'
#!/bin/bash
# Stands in for the binary: cert --out DIR [--host NAME]... (the commands it has)
[ "$1" = cert ] || exit 2
printf '%s\n' "$*" > "$3/args"
printf 'crt\n' > "$3/betterdesk.crt"
printf 'key\n' > "$3/betterdesk.key"
FAKE
    chmod +x "$RUSTDESK_PATH/betterdesk-server"
    openssl() { echo "openssl was used" >> "$TEST_DIR/openssl"; command openssl "$@"; }
    generate_ssl_certificates >/dev/null 2>&1 || fail "the cert command made no certificate"
    [ ! -e "$TEST_DIR/openssl" ] || fail "OpenSSL made the certificate although the binary can"
    grep -q -- '--host 203.0.113.7' "$RUSTDESK_PATH/ssl/args" || fail "the cert command was not given the public address: $(cat "$RUSTDESK_PATH/ssl/args")"
    rm -rf "$TEST_DIR"
)

# L162: a port held by another program is named and left alone; only
# BetterDesk's own leftovers are stopped.
test_port_taken_by_another_program() (
    load
    command -v python3 >/dev/null && command -v ss >/dev/null || { echo "skipped: no python3 or ss"; exit 0; }
    printf 'SIGNAL_PORT=31116\nRELAY_PORT=31117\nCONSOLE_PORT=0\n' > "$CONSOLE_PATH/.env"
    python3 -m http.server 31116 --bind 127.0.0.1 >/dev/null 2>&1 &
    local other=$!
    local i
    for i in $(seq 1 50); do ss -ltnH "sport = :31116" | grep -q . && break; sleep 0.1; done
    local out
    kill_processes_holding_ports > "$TEST_DIR/out" 2>&1
    out=$(cat "$TEST_DIR/out")
    if ! kill -0 "$other" 2>/dev/null; then fail "the installer killed another program on port 31116"; fi
    kill "$other" 2>/dev/null; wait "$other" 2>/dev/null || true
    case "$out" in *31116*python3*) ;; *) fail "the port taken is not named with its program: $out" ;; esac
    [ -n "${FOREIGN_PORT_HOLDERS:-}" ] || fail "the install would go on with a port taken"
    rm -rf "$TEST_DIR"
)

# write_release DIR BRANCH: a release folder for BRANCH (main or dev) with
# two binaries, SHA256SUMS, and manifest.json as release-manifest writes it
# (json.MarshalIndent), its files out of order. RELEASE_COMMIT is the commit.
RELEASE_COMMIT=0123456789abcdef0123456789abcdef01234567
write_release() {
    local dir="$1" branch="$2" channel=stable amd arm
    [ "$branch" = dev ] && channel=development
    mkdir -p "$dir"
    [ -f "$dir/betterdesk-server-linux-amd64" ] || printf 'the new amd64 server\n' > "$dir/betterdesk-server-linux-amd64"
    printf 'the new arm64 server\n' > "$dir/betterdesk-server-linux-arm64"
    amd=$(sha256sum "$dir/betterdesk-server-linux-amd64" | cut -d' ' -f1)
    arm=$(sha256sum "$dir/betterdesk-server-linux-arm64" | cut -d' ' -f1)
    cat > "$dir/manifest.json" <<JSON
{
  "schema_version": 1,
  "product": "betterdesk-server",
  "version": "3.6.0",
  "channel": "$channel",
  "created": "2026-11-22T10:00:00Z",
  "owner": "trybetterdesk",
  "repo": "betterdesk",
  "branch": "$branch",
  "sha": "$RELEASE_COMMIT",
  "files": [
    {
      "path": "betterdesk-server-linux-arm64",
      "sha256": "$arm",
      "size": 21,
      "os": "linux",
      "arch": "arm64"
    },
    {
      "path": "betterdesk-server-linux-amd64",
      "sha256": "$amd",
      "size": 21,
      "os": "linux",
      "arch": "amd64"
    }
  ]
}
JSON
    printf '%s  betterdesk-server-linux-amd64\n%s  betterdesk-server-linux-arm64\n' "$amd" "$arm" > "$dir/SHA256SUMS"
}

# sign_release DIR KEY BRANCH: manifest.sig with KEY over the text the
# release workflow signs (updater.CanonicalPayload), written out here.
sign_release() {
    local dir="$1" amd arm
    amd=$(sha256sum "$dir/betterdesk-server-linux-amd64" | cut -d' ' -f1)
    arm=$(sha256sum "$dir/betterdesk-server-linux-arm64" | cut -d' ' -f1)
    printf 'betterdesk-update-manifest-v1\nowner=trybetterdesk\nrepo=betterdesk\nbranch=%s\nsha=%s\nfiles=2\n%s  betterdesk-server-linux-amd64\n%s  betterdesk-server-linux-arm64\n' \
        "$3" "$RELEASE_COMMIT" "$amd" "$arm" > "$dir/.payload"
    openssl pkeyutl -sign -inkey "$2" -rawin -in "$dir/.payload" -out "$dir/.sig" 2>/dev/null || fail "openssl could not sign"
    { base64 -w0 "$dir/.sig"; echo; } > "$dir/manifest.sig"
    rm -f "$dir/.payload" "$dir/.sig"
}

# A152: a downloaded binary is installed only when the release's manifest
# carries a valid signature of the release key and names that binary for
# the channel. The installed betterdesk-server checks it when it has
# verify-release, else openssl with the key in the script; an openssl that
# cannot check ed25519 is said plainly and the SHA-256 check stays.
test_release_signature() (
    load
    if ! command -v openssl >/dev/null || ! openssl pkeyutl -help 2>&1 | grep -q -- -rawin; then
        echo "skipped: no OpenSSL 3"
        exit 0
    fi
    local rel="$TEST_DIR/release" asset=betterdesk-server-linux-amd64 out rc
    openssl genpkey -algorithm ed25519 -out "$TEST_DIR/key.pem" 2>/dev/null
    openssl genpkey -algorithm ed25519 -out "$TEST_DIR/other.pem" 2>/dev/null
    RELEASE_PUBLIC_KEY_PEM=$(openssl pkey -in "$TEST_DIR/key.pem" -pubout 2>/dev/null)
    print_error() { echo "$1"; }
    print_success() { echo "$1"; }
    # check BRANCH: verify_release_files' status and output
    check() { rc=0; out=$(verify_release_files "$rel" "$asset" "$1" 2>&1) || rc=$?; }

    write_release "$rel" main
    sign_release "$rel" "$TEST_DIR/key.pem" main
    check main
    [ "$rc" = 0 ] || fail "a release signed with the release key was refused ($rc): $out"
    case "$out" in *openssl*) ;; *) fail "the check does not say openssl checked it: $out" ;; esac
    check dev
    [ "$rc" = 1 ] || fail "a stable release passed for the development channel ($rc): $out"

    printf 'another server\n' > "$rel/$asset"
    check main
    [ "$rc" = 1 ] || fail "a binary the manifest does not name passed ($rc): $out"
    rm -f "$rel/$asset"
    write_release "$rel" main
    sign_release "$rel" "$TEST_DIR/key.pem" main
    sed -i "s/$RELEASE_COMMIT/fedcba9876543210fedcba9876543210fedcba98/" "$rel/manifest.json"
    check main
    [ "$rc" = 1 ] || fail "a manifest changed after signing passed ($rc): $out"
    write_release "$rel" main
    sign_release "$rel" "$TEST_DIR/other.pem" main
    check main
    [ "$rc" = 1 ] || fail "a release signed with another key passed ($rc): $out"
    rm -f "$rel/manifest.sig"
    check main
    [ "$rc" = 1 ] || fail "a release without manifest.sig passed ($rc): $out"
    write_release "$rel" dev
    sign_release "$rel" "$TEST_DIR/key.pem" dev
    check dev
    [ "$rc" = 0 ] || fail "a development release signed with the release key was refused ($rc): $out"

    # OpenSSL 1.1 checks no ed25519 signature over raw data
    openssl() { if [ "$1 ${2:-}" = "pkeyutl -help" ]; then echo "Usage: pkeyutl [options] -in file -inkey file"; else echo "OpenSSL 1.1.1w  11 Sep 2023"; fi; }
    rc=0
    verify_release_files "$rel" "$asset" dev > "$TEST_DIR/out" 2>&1 || rc=$?
    unset -f openssl
    [ "$rc" = 2 ] || fail "an openssl without ed25519 gave $rc: $(cat "$TEST_DIR/out")"
    case "$RELEASE_CHECK_PROBLEM" in *"OpenSSL 1.1.1w"*) ;; *) fail "the reason names no OpenSSL version: $RELEASE_CHECK_PROBLEM" ;; esac

    # An installed binary with verify-release checks the release itself,
    # with the release key it carries
    cat > "$RUSTDESK_PATH/betterdesk-server" <<'FAKE'
#!/bin/sh
# Stands in for the binary: verify-release --manifest FILE (the commands it has)
printf '%s\n' "$*" > "$(dirname "$0")/verify-args"
if [ -f "$(dirname "$0")/refuse" ]; then
    echo "betterdesk-server verify-release: the signature of the manifest does not verify against the release key" >&2
    exit 1
fi
echo "verified: betterdesk-server 3.6.0 (0123456), $7"
FAKE
    chmod +x "$RUSTDESK_PATH/betterdesk-server"
    sign_release "$rel" "$TEST_DIR/other.pem" dev
    check dev
    [ "$rc" = 0 ] || fail "the installed binary's check was not used ($rc): $out"
    [ "$(cat "$RUSTDESK_PATH/verify-args")" = "verify-release --manifest $rel/manifest.json --signature $rel/manifest.sig --name $asset $rel/$asset" ] \
        || fail "verify-release ran as: $(cat "$RUSTDESK_PATH/verify-args")"
    case "$out" in *"installed betterdesk-server"*"3.6.0"*) ;; *) fail "the check does not say who checked it: $out" ;; esac
    check main
    [ "$rc" = 1 ] || fail "the installed binary's check let another channel's release pass ($rc): $out"
    touch "$RUSTDESK_PATH/refuse"
    check dev
    [ "$rc" = 1 ] || fail "a release the installed binary refused passed ($rc): $out"
    case "$out" in *"does not verify"*) ;; *) fail "the refusal does not say why: $out" ;; esac
    rm -rf "$TEST_DIR"
)

# A152: the download checks SHA256SUMS and the signature, installs nothing
# when one fails and says so; --skip-verify skips both.
test_download_checks_the_signature() (
    load
    if ! command -v openssl >/dev/null || ! openssl pkeyutl -help 2>&1 | grep -q -- -rawin; then
        echo "skipped: no OpenSSL 3"
        exit 0
    fi
    local rel="$TEST_DIR/release" out
    openssl genpkey -algorithm ed25519 -out "$TEST_DIR/key.pem" 2>/dev/null
    RELEASE_PUBLIC_KEY_PEM=$(openssl pkey -in "$TEST_DIR/key.pem" -pubout 2>/dev/null)
    print_info() { echo "$1"; }
    print_error() { echo "$1"; }
    print_warning() { echo "$1"; }
    release_asset_name() { echo betterdesk-server-linux-amd64; }
    write_release "$rel" main
    sign_release "$rel" "$TEST_DIR/key.pem" main
    unset UPDATE_GITHUB_BRANCH_GIVEN RELEASE_BASE_URL
    UPDATE_GITHUB_BRANCH=main
    # curl: the stable release's files are the ones in $rel
    curl() {
        local a prev="" dest="" url="${!#}"
        for a in "$@"; do [ "$prev" = -o ] && dest="$a"; prev="$a"; done
        if [ -f "$rel/${url##*/}" ]; then cp "$rel/${url##*/}" "$dest"; printf '200'; else printf '404'; fi
    }
    mkdir -p "$TEST_DIR/stage"
    out=$(download_release_binary "$TEST_DIR/stage" 2>&1) || fail "a signed release was not installed: $out"
    [ -f "$TEST_DIR/stage/betterdesk-server" ] || fail "the checked binary is not staged"
    case "$out" in *"SHA-256"*"signature checked"*) ;; *) fail "the download does not say what it checked: $out" ;; esac

    rm -rf "$TEST_DIR/stage" && mkdir -p "$TEST_DIR/stage"
    rm -f "$rel/manifest.sig"
    if out=$(download_release_binary "$TEST_DIR/stage" 2>&1); then fail "a release without a signature was installed: $out"; fi
    [ ! -f "$TEST_DIR/stage/betterdesk-server" ] || fail "a binary without a signature is staged"
    case "$out" in *"no signed manifest"*"--skip-verify"*) ;; *) fail "the refusal does not say what to do: $out" ;; esac
    SKIP_VERIFY=true
    out=$(download_release_binary "$TEST_DIR/stage" 2>&1) || fail "--skip-verify did not install: $out"
    case "$out" in *"checks skipped (--skip-verify)"*) ;; *) fail "--skip-verify does not say what it skipped: $out" ;; esac
    rm -rf "$TEST_DIR"
)

# A152: an update of an install whose binary is older than the release key
# (no verify-release) goes through this installer's own download and
# check, from the repository .env names, instead of the old updater, which
# would take a release without checking its signature.
test_older_binary_updates_through_the_installer() (
    unset UPDATE_GITHUB_OWNER UPDATE_GITHUB_REPO
    load
    do_backup_silent() { :; }
    print_step() { :; }
    print_error() { :; }
    SERVER_BINARY_FILE=""
    FROM_SOURCE=false
    unset RELEASE_BASE_URL
    printf '#!/bin/sh\n# upgrade-env --from FILE (an older binary)\n' > "$RUSTDESK_PATH/betterdesk-server"
    chmod +x "$RUSTDESK_PATH/betterdesk-server"
    printf 'UPDATE_GITHUB_OWNER=acme\nUPDATE_GITHUB_REPO=desk\n' > "$CONSOLE_PATH/.env"
    server_cmd() { echo "server's updater" > "$TEST_DIR/seen"; return 1; }
    curl() { printf '%s\n' "${!#}" >> "$TEST_DIR/urls"; printf '000'; }
    update_go_install >/dev/null 2>&1 || true
    [ ! -e "$TEST_DIR/seen" ] || fail "the older binary's updater ran"
    grep -q '^https://github.com/acme/desk/releases/' "$TEST_DIR/urls" \
        || fail "the installer's download did not ask the repository of .env: $(cat "$TEST_DIR/urls" 2>/dev/null)"
    rm -rf "$TEST_DIR"
)

test_unit_environment
test_exec_args
test_carry_unit_environment
test_write_server_unit
test_node_era_detection
test_kill_stale_processes
test_fresh_env_admin_password
test_database_url_only
test_update_channel_in_env
test_update_channel_before_server_update
test_update_channel_kept_without_one
test_server_folder_mode
test_console_home_copies_no_console_files
test_releases_come_from_the_fork
test_update_keeps_the_admins_ports
test_channel_and_download_errors
test_one_command_install
test_runs_without_term
test_apt_source_failure_goes_on
test_no_stable_release_installs_dev
test_rerun_updates_and_forgets_the_password
test_panel_public_host
test_install_summary
test_uninstall_says_what_stays
test_update_asks_the_fork
test_firewall_ports
test_self_signed_keeps_the_client_api_on_http
test_port_taken_by_another_program
test_release_signature
test_download_checks_the_signature
test_older_binary_updates_through_the_installer
echo "Go installer unit checks passed"
