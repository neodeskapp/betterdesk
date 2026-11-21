#!/usr/bin/env bash
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

test_bounded_go_commands() (
    export GO_COMMAND_HEARTBEAT_INTERVAL=1
    export GO_COMMAND_KILL_AFTER=1
    set --
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/betterdesk.sh"

    if ! _run_go_command_bounded "successful test command" 5 sh -c 'exit 0'; then
        fail "successful bounded command returned non-zero"
    fi

    if _run_go_command_bounded "failed test command" 5 sh -c 'exit 7'; then
        fail "failed bounded command returned success"
    else
        status=$?
    fi
    [ "$status" -eq 7 ] || fail "expected exit code 7, got ${status}"

    if _run_go_command_bounded "timed test command" 1 sh -c 'sleep 10'; then
        fail "timed bounded command returned success"
    else
        status=$?
    fi
    [ "$status" -eq 124 ] || fail "expected timeout code 124, got ${status}"
)

test_dirty_native_clone() (
    local temp_dir fake_bin install_dir repo_dir
    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' EXIT
    fake_bin="$temp_dir/bin"
    install_dir="$temp_dir/install"
    repo_dir="$install_dir/source"
    mkdir -p "$fake_bin" "$repo_dir/.git" "$repo_dir/betterdesk-server/data"
    printf 'local edit\n' > "$repo_dir/betterdesk.sh"
    printf 'runtime state\n' > "$repo_dir/betterdesk-server/data/state.txt"

    cat > "$fake_bin/git" <<'FAKE_GIT'
#!/usr/bin/env bash
set -euo pipefail

if [ "${1:-}" = "-C" ]; then
    shift 2
    case "${1:-}" in
        remote|fetch)
            exit 0
            ;;
        status)
            printf ' M betterdesk.sh\n'
            exit 0
            ;;
        checkout)
            printf 'error: local changes would be overwritten\n' >&2
            exit 1
            ;;
        rev-parse)
            printf 'abc123\n'
            exit 0
            ;;
    esac
fi

if [ "${1:-}" = "clone" ]; then
    if [ "${FAKE_GIT_FAIL_CLONE:-0}" = "1" ]; then
        printf 'simulated clone failure\n' >&2
        exit 1
    fi
    destination="${!#}"
    mkdir -p "$destination/.git" "$destination/betterdesk-server"
    printf '#!/usr/bin/env bash\nexit 0\n' > "$destination/betterdesk.sh"
    exit 0
fi

printf 'unexpected fake git invocation: %s\n' "$*" >&2
exit 1
FAKE_GIT
    chmod +x "$fake_bin/git"
    cat > "$fake_bin/id" <<'FAKE_ID'
#!/usr/bin/env bash
if [ "${1:-}" = "-u" ]; then
    printf '0\n'
    exit 0
fi
exec /usr/bin/id "$@"
FAKE_ID
    chmod +x "$fake_bin/id"

    export PATH="$fake_bin:$PATH"
    export INSTALL_DIR="$install_dir"
    export RELAY_SERVERS="127.0.0.1:21117"
    export BETTERDESK_REPO="example/BetterDesk"
    export BETTERDESK_BRANCH="dev"
    set --
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/install.sh"

    install_native_mode

    [ -d "$repo_dir/.git" ] || fail "fresh clone was not installed"
    [ -f "$repo_dir/betterdesk-server/data/state.txt" ] \
        || fail "runtime Go data was not preserved"
    compgen -G "$install_dir/source.backup.*" > /dev/null \
        || fail "dirty source backup was not preserved"
    backup_dir=""
    for path in "$install_dir"/source.backup.*; do
        if [ -e "$path" ]; then
            backup_dir="$path"
            break
        fi
    done
    [ -f "$backup_dir/betterdesk.sh" ] || fail "original dirty source was not backed up"

    if (
        export FAKE_GIT_FAIL_CLONE=1
        set --
        # shellcheck disable=SC1091
        source "$SCRIPT_ROOT/install.sh"
        install_native_mode
    ); then
        fail "failed clone returned success"
    fi
    [ -d "$repo_dir/.git" ] || fail "original source was not restored after clone failure"

    local piped_output
    if ! piped_output=$(cat "$SCRIPT_ROOT/install.sh" | bash -s -- --native); then
        fail "piped native installer invocation failed: ${piped_output}"
    fi
    [[ "$piped_output" == *"Native installation finished."* ]] \
        || fail "piped native installer did not reach main: ${piped_output}"
)

# UP8: the native install passes the branch it installed to betterdesk.sh
# as the update channel, so an install from dev updates from dev. A branch
# that is no channel passes none, and betterdesk.sh keeps its default.
test_native_update_channel() (
    local temp_dir fake_bin
    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' EXIT
    fake_bin="$temp_dir/bin"
    mkdir -p "$fake_bin"
    cat > "$fake_bin/git" <<'FAKE_GIT'
#!/usr/bin/env bash
if [ "${1:-}" = "-C" ]; then
    [ "${3:-}" = rev-parse ] && printf 'abc123\n'
    exit 0
fi
if [ "${1:-}" = "clone" ]; then
    destination="${!#}"
    mkdir -p "$destination/.git"
    printf '#!/usr/bin/env bash\nprintf "%%s" "${UPDATE_GITHUB_BRANCH:-}" > "%s/channel"\n' "$CHANNEL_DIR" > "$destination/betterdesk.sh"
    exit 0
fi
exit 1
FAKE_GIT
    chmod +x "$fake_bin/git"
    printf '#!/usr/bin/env bash\n[ "${1:-}" = "-u" ] && { printf "0\\n"; exit 0; }\nexec /usr/bin/id "$@"\n' > "$fake_bin/id"
    chmod +x "$fake_bin/id"
    export PATH="$fake_bin:$PATH" RELAY_SERVERS="127.0.0.1:21117" BETTERDESK_REPO="example/BetterDesk"
    export CHANNEL_DIR="$temp_dir"
    unset UPDATE_GITHUB_BRANCH

    local branch expected got
    for branch in dev main feature-x; do
        case "$branch" in
            dev|main) expected="$branch" ;;
            *) expected="" ;;
        esac
        rm -f "$temp_dir/channel"
        (
            export INSTALL_DIR="$temp_dir/install-$branch" BETTERDESK_BRANCH="$branch"
            set --
            # shellcheck disable=SC1091
            source "$SCRIPT_ROOT/install.sh"
            install_native_mode
        ) >/dev/null
        got=$(cat "$temp_dir/channel" 2>/dev/null || echo "(betterdesk.sh did not run)")
        [ "$got" = "$expected" ] \
            || fail "native install from $branch passes update channel '$got', expected '$expected'"
    done
)

# RV83: the migrate tool replaces an existing SQLite destination only with
# -overwrite. The menus pass it after the user agreed, and nothing when
# the user declined; a file the tool wrote goes to the service account.
# The check for an outdated binary asks for -h instead of backing up
# /dev/null.
test_migration_menus() (
    local temp_dir calls owned
    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' EXIT
    calls="$temp_dir/calls"
    owned="$temp_dir/owned"
    mkdir -p "$temp_dir/betterdesk-server/tools/migrate" "$temp_dir/rustdesk"
    cat > "$temp_dir/betterdesk-server/tools/migrate/migrate-linux-amd64" <<FAKE_MIGRATE
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "$calls"
if [ "\${1:-}" = "-h" ]; then
    printf 'Usage of migrate:\n  -mode string\n  -overwrite\n' >&2
fi
exit 0
FAKE_MIGRATE
    chmod +x "$temp_dir/betterdesk-server/tools/migrate/migrate-linux-amd64"

    set --
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/betterdesk.sh"
    SCRIPT_DIR="$temp_dir"
    RUSTDESK_PATH="$temp_dir/rustdesk"
    CONSOLE_PATH="$temp_dir/console"
    LOG_FILE="$temp_dir/log"
    print_header() { :; }
    press_enter() { :; }
    menu_choose() { MENU_CHOICE="$TEST_MENU_CHOICE"; printf '%s\n' "${_menu_items[@]}" > "$temp_dir/items"; }
    own_by_service_user() { printf '%s\n' "$*" >> "$owned"; }

    local src="$RUSTDESK_PATH/db_v2.sqlite3" dst="$RUSTDESK_PATH/betterdesk.sqlite3"
    printf 'db' > "$src"
    printf 'old' > "$dst"

    # run_menu CHOICE INPUT: runs the migration menu with the answers given.
    run_menu() {
        : > "$calls"
        : > "$owned"
        TEST_MENU_CHOICE="$1" do_migrate_database <<< "$2" > /dev/null 2>&1
    }
    migrations() { grep -E -- '-mode (rust2go|nodejs2go|pg2sqlite|sqlite2pg)' "$calls" || true; }

    # RV81: sqlite2pg and pg2sqlite move the whole install, and the menu
    # says so. sqlite2pg replaces a PostgreSQL database with data in it
    # only after the user agreed.
    run_menu 3 $'\npostgres://u@h/db\ny'
    grep -q $'^SQLite -> PostgreSQL\tMove the whole install' "$temp_dir/items" \
        || fail "the menu does not say SQLite -> PostgreSQL moves the whole install: $(cat "$temp_dir/items")"
    grep -q $'^PostgreSQL -> SQLite\tMove the whole install' "$temp_dir/items" \
        || fail "the menu does not say PostgreSQL -> SQLite moves the whole install: $(cat "$temp_dir/items")"
    [ "$(migrations)" = "-mode sqlite2pg -src $src -dst postgres://u@h/db -overwrite" ] \
        || fail "sqlite2pg after an agreed replace ran as: $(migrations)"
    run_menu 3 $'\npostgres://u@h/db\nn'
    [ "$(migrations)" = "-mode sqlite2pg -src $src -dst postgres://u@h/db" ] \
        || fail "sqlite2pg after a declined replace ran as: $(migrations)"

    # pg2sqlite into the existing default file: agreed, then declined
    run_menu 4 $'postgres://u@h/db\n\ny'
    [[ "$(migrations)" == *"-mode pg2sqlite -src postgres://u@h/db -dst $src -overwrite"* ]] \
        || fail "pg2sqlite after an agreed overwrite ran as: $(migrations)"
    grep -qx "$src" "$owned" || fail "pg2sqlite's new file was not handed to the service account"
    run_menu 4 $'postgres://u@h/db\n\nn'
    [ -z "$(migrations)" ] || fail "pg2sqlite ran although the overwrite was declined: $(migrations)"

    # rust2go and nodejs2go into the existing file next to the source
    run_menu 1 $'\n\ny'
    [[ "$(migrations)" == *"-mode rust2go -src $src -dst $dst -overwrite"* ]] \
        || fail "rust2go after an agreed overwrite ran as: $(migrations)"
    grep -qx "$dst" "$owned" || fail "rust2go's new file was not handed to the service account"
    run_menu 1 $'\n\nn'
    [ -z "$(migrations)" ] || fail "rust2go ran although the overwrite was declined: $(migrations)"
    run_menu 2 $'\n\n\ny'
    [[ "$(migrations)" == *"-mode nodejs2go -src $src -dst $dst -overwrite"* ]] \
        || fail "nodejs2go after an agreed overwrite ran as: $(migrations)"
    run_menu 2 $'\n\n\nn'
    [ -z "$(migrations)" ] || fail "nodejs2go ran although the overwrite was declined: $(migrations)"

    # a new file needs no -overwrite, and a PostgreSQL destination no chown
    rm -f "$dst"
    run_menu 1 $'\n\n'
    [[ "$(migrations)" == *"-mode rust2go -src $src -dst $dst" ]] \
        || fail "rust2go into a new file ran as: $(migrations)"
    run_menu 1 $'\npostgres://u@h/db\n'
    [[ "$(migrations)" == *"-dst postgres://u@h/db" ]] || fail "rust2go to PostgreSQL ran as: $(migrations)"
    [ ! -s "$owned" ] || fail "a PostgreSQL destination was chowned: $(cat "$owned")"

    # the check for an outdated binary
    : > "$calls"
    migrate_sqlite_to_postgresql > /dev/null 2>&1 || true
    grep -qx -- "-h" "$calls" || fail "the binary check did not ask for -h: $(cat "$calls")"
    if grep -q "/dev/null" "$calls"; then
        fail "the binary check still backs up /dev/null"
    fi
)

test_bounded_go_commands
test_dirty_native_clone
test_native_update_channel
test_migration_menus
printf 'PASS: native installer regression tests\n'
