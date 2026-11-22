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
    grep -qx "DB_URL=$RUSTDESK_PATH/db_v2.sqlite3" "$CONSOLE_PATH/.env" || fail "DB_URL of a fresh SQLite install"
    # L65: a fresh install starts in Managed; .env is only the first
    # default, a mode saved in the console wins
    grep -qx 'ENROLLMENT_MODE=managed' "$CONSOLE_PATH/.env" || fail "a fresh .env does not start in Managed"
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

test_unit_environment
test_exec_args
test_carry_unit_environment
test_write_server_unit
test_node_era_detection
test_kill_stale_processes
test_fresh_env_admin_password
test_update_channel_in_env
test_update_channel_before_server_update
test_update_channel_kept_without_one
test_server_folder_mode
test_console_home_copies_no_console_files
test_releases_come_from_the_fork
test_update_keeps_the_admins_ports
test_channel_and_download_errors
test_one_command_install
test_rerun_updates_and_forgets_the_password
test_panel_public_host
echo "Go installer unit checks passed"
