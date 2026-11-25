#!/usr/bin/env bash
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

# run_script FILE CODE [NAME=value...]: CODE after FILE is sourced, with
# the variables given and without NO_COLOR, COLORTERM or
# NEODESK_HEADER_SHOWN from outside; its output goes to a pipe
run_script() {
    local file=$1 code=$2 run
    shift 2
    run=$(mktemp)
    printf 'set --\nsource %q\n%s\n' "$file" "$code" > "$run"
    env -u NO_COLOR -u COLORTERM -u NEODESK_HEADER_SHOWN TERM=xterm "$@" bash "$run" < /dev/null 2>&1 | cat
    rm -f "$run"
}

# in_terminal FILE CODE [NAME=value...]: as run_script, in a terminal of
# its own (script(1)); prints what the terminal showed
in_terminal() {
    local file=$1 code=$2 run
    shift 2
    run=$(mktemp)
    printf 'set --\nsource %q\n%s\n' "$file" "$code" > "$run"
    env -u NO_COLOR -u COLORTERM -u NEODESK_HEADER_SHOWN TERM=xterm "$@" script -qec "bash $run" /dev/null < /dev/null | tr -d '\r'
    rm -f "$run"
}

# sgr_colours: the colours in the escape codes of stdin, one per line:
# "basic N" for the 16 colours, "rgb R G B MIN" for a 24-bit text colour,
# MIN being its lowest contrast on black, on PowerShell's blue (#012456)
# and on white
sgr_colours() {
    awk -v esc=$'\033' '
        function lin(c) { c /= 255; return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ^ 2.4 }
        function lum(r, g, b) { return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b) }
        function ratio(a, b) { return a > b ? (a + 0.05) / (b + 0.05) : (b + 0.05) / (a + 0.05) }
        {
            s = $0
            while (match(s, esc "\\[[0-9;]*m")) {
                n = split(substr(s, RSTART + 2, RLENGTH - 3), p, ";")
                s = substr(s, RSTART + RLENGTH)
                for (i = 1; i <= n; i++) {
                    if ((p[i] == 38 || p[i] == 48) && p[i + 1] == 2) {
                        if (p[i] == 38) {
                            l = lum(p[i + 2], p[i + 3], p[i + 4])
                            m = ratio(l, lum(12, 12, 12))
                            if (ratio(l, lum(1, 36, 86)) < m) m = ratio(l, lum(1, 36, 86))
                            if (ratio(l, lum(255, 255, 255)) < m) m = ratio(l, lum(255, 255, 255))
                            printf "rgb %s %s %s %.2f\n", p[i + 2], p[i + 3], p[i + 4], m
                        }
                        i += 4
                    } else if (p[i] != "" && p[i] + 0 >= 30) {
                        print "basic", p[i] + 0
                    }
                }
            }
        }'
}

# blue_or_magenta: stdin (from sgr_colours) has a blue or magenta of the
# 16 colours (34, 35, 44, 45, 94, 95, 104, 105)
blue_or_magenta() {
    awk '$1 == "basic" && ($2 % 10 == 4 || $2 % 10 == 5) { found = 1 } END { exit !found }'
}

# check_header NAME OUT TITLE: OUT is NAME's header drawn in a terminal:
# the logo in 15 lines of half blocks with TITLE on its 13th, under the
# wordmark, no box characters, no blue or magenta, no line wider than 119 columns or
# ending in spaces
check_header() {
    local name=$1 out=$2 title=$3 text line box LC_ALL=C.UTF-8
    text=$(printf '%s\n' "$out" | sed $'s/\033\\[[0-9;?]*[A-Za-z]//g')
    for box in ═ ║ ╔ ╗ ╚ ╝ ─ │ ┌ ┐ └ ┘ ╭ ╮ ╯ ╰; do
        case "$text" in *"$box"*) fail "$name's header draws a box: $text" ;; esac
    done
    [ "$(printf '%s\n' "$text" | grep -cF -e ▀ -e ▄ -e █)" = 15 ] || fail "$name's header has no logo of 15 lines: $text"
    [ "$(printf '%s\n' "$text" | grep -F -e ▀ -e ▄ -e █ | sed -n 13p | sed 's/^[▀▄█]* *//')" = "$title" ] \
        || fail "$name's header does not have $title beside the mark under the wordmark: $text"
    while IFS= read -r line; do
        [ "${#line}" -le 119 ] || fail "$name's header has a line of ${#line} columns: $line"
        case "$line" in *' ') fail "$name's header has a line that ends in spaces: '$line'" ;; esac
    done <<< "$text"
    if printf '%s\n' "$out" | sgr_colours | blue_or_magenta; then
        fail "$name's header uses blue or magenta: $(printf '%q' "$out")"
    fi
}

# check_output NAME TITLE HELPERS [NAME=value...]: NAME's header is the
# logo with TITLE in a terminal of 120 columns, "NeoDesk TITLE" on one
# line in a terminal narrower than the logo, and one plain line "NeoDesk
# TITLE" without a terminal or with NO_COLOR, with no escape codes at all
# then. The status helpers (HELPERS, code that calls each) use no blue or
# magenta, and their 24-bit colours keep 3.5:1 on black, on PowerShell's
# blue and on white.
check_output() {
    local name=$1 title=$2 helpers=$3 file="$SCRIPT_ROOT/install/$1" out colours
    shift 3
    out=$(run_script "$file" "print_header; $helpers" "$@")
    case "$out" in *$'\033'*) fail "$name has escape codes without a terminal: $(printf '%q' "$out")" ;; esac
    [ "$(printf '%s\n' "$out" | head -1)" = "NeoDesk $title" ] || fail "without a terminal $name's header is not one plain line: $out"
    out=$(in_terminal "$file" "print_header; $helpers" NO_COLOR=1 "$@")
    case "$out" in *$'\033'*) fail "$name has escape codes with NO_COLOR: $(printf '%q' "$out")" ;; esac
    [ "$(printf '%s\n' "$out" | head -1)" = "NeoDesk $title" ] || fail "with NO_COLOR $name's header is not one plain line: $out"

    out=$(in_terminal "$file" print_header COLUMNS=120 "$@")
    check_header "$name" "$out" "$title"
    out=$(in_terminal "$file" "$helpers" "$@")
    if printf '%s\n' "$out" | sgr_colours | blue_or_magenta; then
        fail "a status helper of $name uses blue or magenta: $(printf '%q' "$out")"
    fi

    out=$(in_terminal "$file" print_header COLORTERM=truecolor COLUMNS=120 "$@")
    check_header "$name" "$out" "$title"
    case "$out" in *$'\033[38;2;88;166;255m'*) ;; *) fail "$name's mark is not in its 24-bit colours: $(printf '%q' "$out")" ;; esac
    out=$(in_terminal "$file" print_header COLUMNS=118 "$@")
    [ "$(printf '%s\n' "$out" | sed $'s/\033\\[[0-9;?]*[A-Za-z]//g' | grep -v '^$')" = "NeoDesk $title" ] \
        || fail "in a terminal of 118 columns $name's header is not one line: $(printf '%q' "$out")"
    out=$(in_terminal "$file" "$helpers" COLORTERM=truecolor "$@")
    colours=$(printf '%s\n' "$out" | sgr_colours)
    [ "$(printf '%s\n' "$colours" | grep -c '^rgb ')" -ge 4 ] || fail "the status helpers of $name have no 24-bit colours: $(printf '%q' "$out")"
    if printf '%s\n' "$colours" | blue_or_magenta || printf '%s\n' "$colours" | awk '$1 == "rgb" && $5 < 3.5 { found = 1 } END { exit !found }'; then
        fail "a status colour of $name is blue, magenta or hard to read: $colours"
    fi
}

# The installers' header and status colours (check_output). install.sh's
# header stands for the managers it runs: neodesk-docker.sh leaves its
# own out then.
test_headers_and_colours() (
    local version out
    version=$(sed -n 's/^VERSION="\(.*\)"$/\1/p' "$SCRIPT_ROOT/install/neodesk.sh")
    check_output neodesk.sh "Server installer $version" \
        'print_success s; print_error e; print_warning w; print_info i; print_step p'
    version=$(sed -n 's/^VERSION="\(.*\)"$/\1/p' "$SCRIPT_ROOT/install/neodesk-docker.sh")
    check_output neodesk-docker.sh "Docker installer $version" \
        'step s; ok o; warn w; (die e) || true' NEODESK_DOCKER_SOURCED=1
    version=$(sed -n 's/^NEODESK_VERSION="${NEODESK_VERSION:-\(.*\)}"$/\1/p' "$SCRIPT_ROOT/install/install.sh")
    check_output install.sh "Installer $version" 'log l; ok o; warn w; (die e) || true'

    out=$(run_script "$SCRIPT_ROOT/install/install.sh" 'print_header > /dev/null; printenv NEODESK_HEADER_SHOWN')
    [ "$out" = 1 ] || fail "install.sh does not tell the managers it showed the header: $out"
    out=$(in_terminal "$SCRIPT_ROOT/install/neodesk-docker.sh" print_header NEODESK_DOCKER_SOURCED=1 NEODESK_HEADER_SHOWN=1)
    [ -z "$out" ] || fail "neodesk-docker.sh shows its header after install.sh's: $out"
)

test_bounded_go_commands() (
    export GO_COMMAND_HEARTBEAT_INTERVAL=1
    export GO_COMMAND_KILL_AFTER=1
    set --
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/install/neodesk.sh"

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
    mkdir -p "$fake_bin" "$repo_dir/.git" "$repo_dir/neodesk-server/data"
    printf 'local edit\n' > "$repo_dir/neodesk.sh"
    printf 'runtime state\n' > "$repo_dir/neodesk-server/data/state.txt"

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
            printf ' M neodesk.sh\n'
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
    mkdir -p "$destination/.git" "$destination/neodesk-server"
    printf '#!/usr/bin/env bash\nexit 0\n' > "$destination/neodesk.sh"
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
    export NEODESK_REPO="example/NeoDesk"
    export NEODESK_BRANCH="main"
    set --
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/install/install.sh"

    install_native_mode

    [ -d "$repo_dir/.git" ] || fail "fresh clone was not installed"
    [ -f "$repo_dir/neodesk-server/data/state.txt" ] \
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
    [ -f "$backup_dir/neodesk.sh" ] || fail "original dirty source was not backed up"

    if (
        export FAKE_GIT_FAIL_CLONE=1
        set --
        # shellcheck disable=SC1091
        source "$SCRIPT_ROOT/install/install.sh"
        install_native_mode
    ); then
        fail "failed clone returned success"
    fi
    [ -d "$repo_dir/.git" ] || fail "original source was not restored after clone failure"

    local piped_output
    if ! piped_output=$(cat "$SCRIPT_ROOT/install/install.sh" | bash -s -- --native); then
        fail "piped native installer invocation failed: ${piped_output}"
    fi
    [[ "$piped_output" == *"Native installation finished."* ]] \
        || fail "piped native installer did not reach main: ${piped_output}"
)

# UP8: the native install passes the release channel it was given
# (--channel) to neodesk.sh. The branch only names the installer's
# files: every branch, main included, passes no channel, and neodesk.sh
# keeps its default.
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
    printf '#!/usr/bin/env bash\nprintf "%%s" "${UPDATE_GITHUB_BRANCH:-}" > "%s/channel"\n' "$CHANNEL_DIR" > "$destination/neodesk.sh"
    exit 0
fi
exit 1
FAKE_GIT
    chmod +x "$fake_bin/git"
    printf '#!/usr/bin/env bash\n[ "${1:-}" = "-u" ] && { printf "0\\n"; exit 0; }\nexec /usr/bin/id "$@"\n' > "$fake_bin/id"
    chmod +x "$fake_bin/id"
    export PATH="$fake_bin:$PATH" RELAY_SERVERS="127.0.0.1:21117" NEODESK_REPO="example/NeoDesk"
    export CHANNEL_DIR="$temp_dir"
    unset UPDATE_GITHUB_BRANCH

    local branch expected got
    for branch in main dev feature-x; do
        expected=""
        rm -f "$temp_dir/channel"
        (
            export INSTALL_DIR="$temp_dir/install-$branch" NEODESK_BRANCH="$branch"
            set --
            # shellcheck disable=SC1091
            source "$SCRIPT_ROOT/install/install.sh"
            install_native_mode
        ) >/dev/null
        got=$(cat "$temp_dir/channel" 2>/dev/null || echo "(neodesk.sh did not run)")
        [ "$got" = "$expected" ] \
            || fail "native install from $branch passes update channel '$got', expected '$expected'"
    done

    # Without --branch the default branch's files install and neodesk.sh
    # picks the channel (stable, or dev while there is no stable release);
    # --channel names one
    local args
    for args in "" "--channel dev" "--channel stable"; do
        case "$args" in
            *dev) expected=dev ;;
            *stable) expected=main ;;
            *) expected="" ;;
        esac
        rm -f "$temp_dir/channel"
        (
            unset NEODESK_BRANCH
            export INSTALL_DIR="$temp_dir/install-default"
            # shellcheck disable=SC2086
            set -- $args
            # shellcheck disable=SC1091
            source "$SCRIPT_ROOT/install/install.sh"
            [ "$NEODESK_BRANCH" = main ] || fail "install.sh installs the files of $NEODESK_BRANCH"
            install_native_mode
        ) >/dev/null
        got=$(cat "$temp_dir/channel" 2>/dev/null || echo "(neodesk.sh did not run)")
        [ "$got" = "$expected" ] || fail "install.sh $args passes update channel '$got', expected '$expected'"
    done
)

# RV83: the migrate command replaces an existing SQLite destination only
# with -overwrite. The menus pass it after the user agreed, and nothing
# when the user declined; a file it wrote goes to the service account.
# L71: the installed binary's migrate command is the tool; without one the
# menu says to update first.
test_migration_menus() (
    local temp_dir calls owned
    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' EXIT
    calls="$temp_dir/calls"
    owned="$temp_dir/owned"
    mkdir -p "$temp_dir/rustdesk"
    cat > "$temp_dir/rustdesk/neodesk-server" <<FAKE_SERVER
#!/usr/bin/env bash
# Stands in for the binary: migrate -mode sqlite2pg|pg2sqlite (its commands)
[ "\${1:-}" = migrate ] && shift
printf '%s\n' "\$*" >> "$calls"
exit 0
FAKE_SERVER
    chmod +x "$temp_dir/rustdesk/neodesk-server"

    set --
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/install/neodesk.sh"
    SCRIPT_DIR="$temp_dir"
    RUSTDESK_PATH="$temp_dir/rustdesk"
    CONSOLE_PATH="$temp_dir/console"
    LOG_FILE="$temp_dir/log"
    print_header() { :; }
    press_enter() { :; }
    menu_choose() { MENU_CHOICE="$TEST_MENU_CHOICE"; printf '%s\n' "${_menu_items[@]}" > "$temp_dir/items"; }
    own_by_service_user() { printf '%s\n' "$*" >> "$owned"; }

    local src="$RUSTDESK_PATH/db_v2.sqlite3" dst="$RUSTDESK_PATH/neodesk.sqlite3"
    printf 'db' > "$src"
    printf 'old' > "$dst"

    # run_menu CHOICE INPUT: runs the migration menu with the answers given.
    run_menu() {
        : > "$calls"
        : > "$owned"
        TEST_MENU_CHOICE="$1" do_migrate_database <<< "$2" > /dev/null 2>&1
    }
    migrations() { grep -E -- '-mode (rust2go|pg2sqlite|sqlite2pg)' "$calls" || true; }

    # RV81: sqlite2pg and pg2sqlite move the whole install, and the menu
    # says so. sqlite2pg replaces a PostgreSQL database with data in it
    # only after the user agreed.
    run_menu 2 $'\npostgres://u@h/db\ny'
    [ "$(cut -f1 "$temp_dir/items" | paste -sd '|')" = "Rust -> Go|SQLite -> PostgreSQL|PostgreSQL -> SQLite|Backup|Back" ] \
        || fail "the migration menu offers: $(cut -f1 "$temp_dir/items" | paste -sd '|')"
    grep -q $'^SQLite -> PostgreSQL\tMove the whole install' "$temp_dir/items" \
        || fail "the menu does not say SQLite -> PostgreSQL moves the whole install: $(cat "$temp_dir/items")"
    grep -q $'^PostgreSQL -> SQLite\tMove the whole install' "$temp_dir/items" \
        || fail "the menu does not say PostgreSQL -> SQLite moves the whole install: $(cat "$temp_dir/items")"
    [ "$(migrations)" = "-mode sqlite2pg -src $src -dst postgres://u@h/db -overwrite" ] \
        || fail "sqlite2pg after an agreed replace ran as: $(migrations)"
    run_menu 2 $'\npostgres://u@h/db\nn'
    [ "$(migrations)" = "-mode sqlite2pg -src $src -dst postgres://u@h/db" ] \
        || fail "sqlite2pg after a declined replace ran as: $(migrations)"

    # L72: a move of the install's own database points .env at the new one
    # and restarts the server
    mkdir -p "$CONSOLE_PATH"
    printf 'SESSION_SECRET=kept\nDB_TYPE=sqlite\nDATABASE_URL=\nDB_URL=%s\n' "$src" > "$CONSOLE_PATH/.env"
    systemctl() { printf 'systemctl %s\n' "$*" >> "$calls"; }
    run_menu 2 $'\npostgres://u@h/db\nn'
    grep -qx 'DB_TYPE=postgres' "$CONSOLE_PATH/.env" && grep -qx 'DATABASE_URL=postgres://u@h/db' "$CONSOLE_PATH/.env" \
        && grep -qx 'DB_URL=postgres://u@h/db' "$CONSOLE_PATH/.env" || fail "after the menu's move .env does not point at PostgreSQL: $(cat "$CONSOLE_PATH/.env")"
    grep -qx 'SESSION_SECRET=kept' "$CONSOLE_PATH/.env" || fail "the menu's move lost a line of .env"
    grep -q 'systemctl restart neodesk-server' "$calls" || fail "the server was not restarted after the move"
    run_menu 3 $'postgres://u@h/db\n\ny'
    grep -qx 'DB_TYPE=sqlite' "$CONSOLE_PATH/.env" && grep -qxF "DB_URL=$src" "$CONSOLE_PATH/.env" \
        || fail "after the menu's move back .env does not point at SQLite: $(cat "$CONSOLE_PATH/.env")"
    rm -f "$CONSOLE_PATH/.env"
    unset -f systemctl

    # pg2sqlite into the existing default file: agreed, then declined
    run_menu 3 $'postgres://u@h/db\n\ny'
    [[ "$(migrations)" == *"-mode pg2sqlite -src postgres://u@h/db -dst $src -overwrite"* ]] \
        || fail "pg2sqlite after an agreed overwrite ran as: $(migrations)"
    grep -qx "$src" "$owned" || fail "pg2sqlite's new file was not handed to the service account"
    run_menu 3 $'postgres://u@h/db\n\nn'
    [ -z "$(migrations)" ] || fail "pg2sqlite ran although the overwrite was declined: $(migrations)"

    # rust2go into the existing file next to the source
    run_menu 1 $'\n\ny'
    [[ "$(migrations)" == *"-mode rust2go -src $src -dst $dst -overwrite"* ]] \
        || fail "rust2go after an agreed overwrite ran as: $(migrations)"
    grep -qx "$dst" "$owned" || fail "rust2go's new file was not handed to the service account"
    run_menu 1 $'\n\nn'
    [ -z "$(migrations)" ] || fail "rust2go ran although the overwrite was declined: $(migrations)"

    # a new file needs no -overwrite, and a PostgreSQL destination no chown
    rm -f "$dst"
    run_menu 1 $'\n\n'
    [[ "$(migrations)" == *"-mode rust2go -src $src -dst $dst" ]] \
        || fail "rust2go into a new file ran as: $(migrations)"
    run_menu 1 $'\npostgres://u@h/db\n'
    [[ "$(migrations)" == *"-dst postgres://u@h/db" ]] || fail "rust2go to PostgreSQL ran as: $(migrations)"
    [ ! -s "$owned" ] || fail "a PostgreSQL destination was chowned: $(cat "$owned")"

    # without the migrate command (a binary older than it), nothing runs
    printf '#!/bin/sh\nprintf "%%s\\n" "$*" >> "%s"\n' "$calls" > "$RUSTDESK_PATH/neodesk-server"
    printf 'old' > "$dst"
    run_menu 1 $'\n\ny'
    [ ! -s "$calls" ] || fail "a binary without the migrate command ran: $(cat "$calls")"
)

# RV81: the install's switch to PostgreSQL moves the whole SQLite install
# with sqlite2pg, passes -overwrite only for a database it created itself
# or after the user agreed, and points .env at PostgreSQL only after the
# move succeeded; when it fails the install stays on its SQLite file.
test_postgresql_switch() (
    local temp_dir calls
    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' EXIT
    calls="$temp_dir/calls"
    mkdir -p "$temp_dir/rustdesk" "$temp_dir/console"
    cat > "$temp_dir/rustdesk/neodesk-server" <<FAKE_SERVER
#!/usr/bin/env bash
# Stands in for the binary: migrate -mode sqlite2pg|pg2sqlite (its commands)
[ "\${1:-}" = migrate ] && shift
printf '%s\n' "\$*" >> "$calls"
case "\${1:-}\${2:-}" in
    -modesqlite2pg) exit "\$(cat "$temp_dir/exit")" ;;
esac
exit 0
FAKE_SERVER
    chmod +x "$temp_dir/rustdesk/neodesk-server"

    set --
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/install/neodesk.sh"
    SCRIPT_DIR="$temp_dir"
    RUSTDESK_PATH="$temp_dir/rustdesk"
    CONSOLE_PATH="$temp_dir/console"
    LOG_FILE="$temp_dir/log"
    local src="$RUSTDESK_PATH/db_v2.sqlite3" env="$CONSOLE_PATH/.env"
    local uri="postgres://u:p&q@h/db?sslmode=disable"
    printf 'db' > "$src"

    # run_switch EXIT CREATED AUTO INPUT: the switch as the install runs it,
    # with the tool exiting EXIT and .env written for PostgreSQL by the
    # console folder's setup.
    run_switch() {
        : > "$calls"
        printf '%s' "$1" > "$temp_dir/exit"
        printf 'SESSION_SECRET=kept\nDB_TYPE=postgres\nDATABASE_URL=%s\nDB_URL=%s\n' "$uri" "$uri" > "$env"
        USE_POSTGRESQL=true POSTGRESQL_URI="$uri" POSTGRESQL_DB_CREATED="$2" AUTO_MODE="$3"
        migrate_sqlite_to_postgresql <<< "$4" > /dev/null 2>&1
    }
    moves() { grep -E -- '-mode sqlite2pg' "$calls" || true; }

    # a database the install just created
    run_switch 0 true true ""
    [ "$(moves)" = "-mode sqlite2pg -src $src -dst $uri -overwrite" ] \
        || fail "the switch into a database it created ran as: $(moves)"
    grep -q -- "-mode backup -src $src" "$calls" || fail "the switch made no backup first: $(cat "$calls")"
    [ "$USE_POSTGRESQL" = true ] || fail "after a move the install does not use PostgreSQL"
    grep -qxF "DB_URL=$uri" "$env" && grep -qx "DB_TYPE=postgres" "$env" && grep -qxF "DATABASE_URL=$uri" "$env" \
        || fail "after a move .env does not point at PostgreSQL: $(cat "$env")"
    grep -qx "SESSION_SECRET=kept" "$env" || fail "the switch lost a line of .env"

    # a database that was there: -overwrite only after a yes
    run_switch 0 false true ""
    [ "$(moves)" = "-mode sqlite2pg -src $src -dst $uri" ] \
        || fail "the unattended switch into an existing database ran as: $(moves)"
    run_switch 0 false false $'y\ny'
    [ "$(moves)" = "-mode sqlite2pg -src $src -dst $uri -overwrite" ] \
        || fail "the switch after an agreed replace ran as: $(moves)"
    run_switch 0 false false $'y\nn'
    [ "$(moves)" = "-mode sqlite2pg -src $src -dst $uri" ] \
        || fail "the switch after a declined replace ran as: $(moves)"

    # L60: a declined move keeps the install on SQLite
    run_switch 0 true false $'n'
    [ -z "$(moves)" ] || fail "the switch moved the data although the move was declined: $(moves)"
    [ "$USE_POSTGRESQL" = false ] && [ -z "$POSTGRESQL_URI" ] \
        || fail "after a declined move the install still uses PostgreSQL ($USE_POSTGRESQL, $POSTGRESQL_URI)"
    grep -qxF "DB_URL=$src" "$env" && grep -qx "DB_TYPE=sqlite" "$env" && grep -qx "DATABASE_URL=" "$env" \
        || fail "after a declined move .env does not point at the SQLite file: $(cat "$env")"

    # a failed move leaves the install on SQLite
    run_switch 1 true true ""
    [ "$USE_POSTGRESQL" = false ] && [ -z "$POSTGRESQL_URI" ] \
        || fail "after a failed move the install still uses PostgreSQL ($USE_POSTGRESQL, $POSTGRESQL_URI)"
    grep -qxF "DB_URL=$src" "$env" && grep -qx "DB_TYPE=sqlite" "$env" && grep -qx "DATABASE_URL=" "$env" \
        || fail "after a failed move .env does not point at the SQLite file: $(cat "$env")"

    # L71: a binary without the migrate command moves nothing, and the
    # install stays on SQLite
    printf '#!/bin/sh\nprintf "%%s\\n" "$*" >> "%s"\n' "$calls" > "$RUSTDESK_PATH/neodesk-server"
    run_switch 0 true true ""
    [ ! -s "$calls" ] || fail "a binary without the migrate command ran: $(cat "$calls")"
    [ "$USE_POSTGRESQL" = false ] || fail "without the migrate command the install uses PostgreSQL"
)

# L31: the one-line installer clones and downloads from the fork.
test_install_sh_uses_the_fork() (
    unset NEODESK_REPO NEODESK_RAW_BASE NEODESK_BRANCH
    set --
    # shellcheck disable=SC1091
    source "$SCRIPT_ROOT/install/install.sh"
    [ "$NEODESK_REPO" = neodeskapp/neodesk ] || fail "install.sh clones $NEODESK_REPO"
    case "$NEODESK_RAW_BASE" in
        https://raw.githubusercontent.com/neodeskapp/neodesk/*) ;;
        *) fail "install.sh downloads from $NEODESK_RAW_BASE" ;;
    esac
    # The one-liners and the installers' own downloads name the only
    # branch, main; there is no dev branch, and nothing downloads from
    # upstream's repository
    [ "$NEODESK_RAW_BASE" = https://raw.githubusercontent.com/neodeskapp/neodesk/main/install ] \
        || fail "install.sh downloads from $NEODESK_RAW_BASE"
    local hits
    hits=$(cd "$SCRIPT_ROOT" && grep -nE 'raw\.githubusercontent\.com/(neodeskapp/neodesk/dev/|UNITRONIX/BetterDesk/)|github\.com/neodeskapp/neodesk/(blob|tree)/dev/' \
        install/install.sh install/neodesk.sh install/neodesk.ps1 install/neodesk-docker.sh install/docker-compose*.yml README.md SECURITY.md || true)
    [ -z "$hits" ] || fail "one-liners name a branch without the current installers: $hits"
    local docker_base
    docker_base=$(
        unset NEODESK_RAW_BASE NEODESK_BRANCH
        NEODESK_DOCKER_SOURCED=1
        # shellcheck disable=SC1091
        source "$SCRIPT_ROOT/install/neodesk-docker.sh"
        printf '%s\n' "$NEODESK_RAW_BASE"
    )
    [ "$docker_base" = https://raw.githubusercontent.com/neodeskapp/neodesk/main/install ] \
        || fail "neodesk-docker.sh downloads from $docker_base"
    grep -qF '$script:INSTALLER_REF = if ($env:INSTALLER_REF) { $env:INSTALLER_REF } else { "main" }' "$SCRIPT_ROOT/install/neodesk.ps1" \
        || fail "neodesk.ps1 does not fetch itself from main"
)

test_bounded_go_commands
test_dirty_native_clone
test_native_update_channel
test_migration_menus
test_postgresql_switch
test_install_sh_uses_the_fork
test_headers_and_colours
printf 'PASS: native installer regression tests\n'
