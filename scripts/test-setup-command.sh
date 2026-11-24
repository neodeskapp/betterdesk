#!/usr/bin/env bash
# The installer's commands on a real install (L431): neodesk-setup is the
# installer's manager, and neodesk is the command line, a copy of the
# server binary that is root's own and that every user may run (the
# server folder stays closed), and that hands the installer's options to
# neodesk-setup. A fresh install with the server built from this checkout,
# the command line as a normal user (its help, a list of devices with an
# API token, and what --update says), the installer's options and a bare
# `sudo neodesk` through the new name, an update by neodesk-setup of an
# install whose neodesk is still the link of an earlier build, the update
# of an install whose /usr/local/sbin/neodesk is the old manager
# (3.6.0-rc.4), an update whose binary does not start (it goes back to the
# binary from before), and the uninstall. The backups go to
# /opt/neodesk-backups, and the old manager's backups in
# /opt/rustdesk-backups move there, with nothing left at the old place
# (L446).
#
# It installs NeoDesk on this machine and removes it again, so it runs as
# root on a machine that can be thrown away (CI, a sandbox), with systemd
# and the network for the packages:
#
#   sudo bash scripts/test-setup-command.sh
#
# NEODESK_TEST_BINARY: a neodesk-server built from this checkout (else go
# builds one). OLD_MANAGER: the old manager's file (else 3.6.0-rc.4's
# neodesk.sh is downloaded).
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SETUP=/usr/local/sbin/neodesk-setup
CLI=/usr/local/bin/neodesk
OLD=/usr/local/sbin/neodesk
BIN=/opt/neodesk/neodesk-server
BACKUPS=/opt/neodesk-backups
OLD_BACKUPS=/opt/rustdesk-backups
OLD_MANAGER_URL=https://raw.githubusercontent.com/neodeskapp/neodesk/v3.6.0-rc.4/neodesk.sh
# A user without root, for the command line
TEST_USER=neodesk-cli-test

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

[ "$(id -u)" = 0 ] || fail "run this as root, on a machine that can be thrown away"
[ -d /run/systemd/system ] || fail "this needs systemd"
if [ -e "$BIN" ] || [ -e "$SETUP" ] || [ -e "$OLD" ] || [ -e "$BACKUPS" ] || [ -e "$OLD_BACKUPS" ]; then
    fail "NeoDesk is installed here already; this test needs a machine without it"
fi

WORK=$(mktemp -d)
export NO_COLOR=1

# The server binary of this checkout
if [ -n "${NEODESK_TEST_BINARY:-}" ]; then
    cp "$NEODESK_TEST_BINARY" "$WORK/neodesk-server"
else
    (cd "$SCRIPT_ROOT/neodesk-server" && CGO_ENABLED=0 go build -o "$WORK/neodesk-server" .) \
        || fail "neodesk-server did not build"
fi
chmod 755 "$WORK/neodesk-server"

# The old manager: neodesk.sh of 3.6.0-rc.4, which installed itself as
# /usr/local/sbin/neodesk
if [ -n "${OLD_MANAGER:-}" ]; then
    cp "$OLD_MANAGER" "$WORK/old-manager"
else
    curl -fsSL --retry 3 -o "$WORK/old-manager" "$OLD_MANAGER_URL" || fail "could not download $OLD_MANAGER_URL"
fi
grep -qx 'MANAGER_COMMAND="/usr/local/sbin/neodesk"' "$WORK/old-manager" || fail "$WORK/old-manager is not the old manager"

# What an update saves as the manager: this checkout's neodesk.sh with
# its top made longer, so a copy written over a running script would
# break that script's run
{
    head -n 1 "$SCRIPT_ROOT/neodesk.sh"
    for _ in $(seq 1 300); do printf '# The newest installer, as the update fetches it\n'; done
    tail -n +2 "$SCRIPT_ROOT/neodesk.sh"
} > "$WORK/newest.sh"

# curl: the newest installer for the address the manager fetches itself
# from, the real curl for everything else
mkdir -p "$WORK/stub"
cat > "$WORK/stub/curl" <<EOF
#!/bin/bash
url="\${!#}"
case "\$url" in
    https://raw.githubusercontent.com/*/neodesk.sh)
        dest="" prev=""
        for a in "\$@"; do [ "\$prev" = -o ] && dest="\$a"; prev="\$a"; done
        printf '%s\n' "\$url" >> "$WORK/fetched"
        cp "$WORK/newest.sh" "\$dest"
        exit 0
        ;;
esac
exec "$(command -v curl)" "\$@"
EOF
chmod 755 "$WORK/stub/curl"
export PATH="$WORK/stub:$PATH"

# console_address: the console's address on this machine, HTTPS or HTTP
console_address() {
    local a
    for a in https://127.0.0.1:5443 http://127.0.0.1:5000; do
        if curl -ksf -o /dev/null --max-time 5 "$a/health"; then
            printf '%s\n' "$a"
            return 0
        fi
    done
    return 1
}

# make_token CONSOLE: prints a new API token that may list devices. The
# admin gets a password of the test's own (--reset-password), signs in to
# the console and makes the token there. Neither shows in an output.
make_token() {
    local console=$1 pw jar csrf
    pw=$(openssl rand -hex 16)
    ADMIN_PASSWORD="$pw" neodesk --reset-password < /dev/null > /dev/null 2>&1 || { echo "the password reset failed" >&2; return 1; }
    jar="$WORK/cookies"
    rm -f "$jar"
    curl -ksf -c "$jar" -b "$jar" -o /dev/null "$console/api/auth/login-page" || { echo "no login page" >&2; return 1; }
    csrf=$(awk '$6 ~ /^__csrf/ { print $7 }' "$jar" | head -n 1 | sed 's/%7C.*//; s/|.*//')
    printf '{"username":"admin","password":"%s"}' "$pw" > "$WORK/login.json"
    curl -ksf -c "$jar" -b "$jar" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' \
        --data @"$WORK/login.json" -o /dev/null "$console/api/auth/login" || { echo "the admin could not sign in" >&2; return 1; }
    rm -f "$WORK/login.json"
    curl -ksf -c "$jar" -b "$jar" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' \
        --data '{"name":"installer test","permissions":["device.view"],"expires_in_days":1}' \
        -o "$WORK/token.json" "$console/api/panel/api-tokens" || { echo "the console made no token" >&2; return 1; }
    sed -n 's/.*"token":"\([^"]*\)".*/\1/p' "$WORK/token.json"
    rm -f "$WORK/token.json" "$jar"
}

# ran_cleanly NAME LOG: LOG has no sign of a script that broke while it ran
ran_cleanly() {
    if grep -nE 'syntax error|unexpected end of file|command not found|line [0-9]+: .*: No such file' "$2"; then
        fail "$1 did not run cleanly (lines above); its log: $2"
    fi
}

# new_layout WHEN NEWEST: neodesk-setup is NEWEST, neodesk is a copy of
# the server binary, 0755 and root's own, and nothing is at
# /usr/local/sbin/neodesk
new_layout() {
    [ -x "$SETUP" ] || fail "$1: no $SETUP"
    cmp -s "$SETUP" "$2" || fail "$1: $SETUP is not $2"
    [ -f "$CLI" ] && [ ! -L "$CLI" ] || fail "$1: $CLI is no file of its own: $(ls -l "$CLI" 2>&1)"
    [ "$(stat -c '%U:%G %a' "$CLI")" = "root:root 755" ] || fail "$1: $CLI is $(stat -c '%U:%G %a' "$CLI")"
    cmp -s "$CLI" "$BIN" || fail "$1: $CLI is not the server binary $BIN"
    [ ! -e "$OLD" ] || fail "$1: $OLD is still there: $(head -c 300 "$OLD")"
    [ "$(command -v neodesk)" = "$CLI" ] || fail "$1: neodesk is $(command -v neodesk)"
}

echo "== fresh install"
RELAY_SERVERS=127.0.0.1 bash "$SCRIPT_ROOT/neodesk.sh" --auto --binary "$WORK/neodesk-server" \
    < /dev/null > "$WORK/install.log" 2>&1 || fail "the install failed: $(tail -n 40 "$WORK/install.log")"
ran_cleanly "the install" "$WORK/install.log"
hash -r
new_layout "after a fresh install" "$SCRIPT_ROOT/neodesk.sh"

echo "== the command line for every user"
# The server folder stays closed; the command line is outside it
[ "$(stat -c '%a' "$(dirname "$BIN")")" = 2770 ] || fail "the server folder has mode $(stat -c '%a' "$(dirname "$BIN")")"
id "$TEST_USER" >/dev/null 2>&1 || useradd -m "$TEST_USER" || fail "could not add the user $TEST_USER"
# The user's own HOME, without a config folder of the account that ran
# this script (a runner keeps XDG_CONFIG_HOME through sudo and runuser)
as_user() { runuser -u "$TEST_USER" -- env -u XDG_CONFIG_HOME HOME="/home/$TEST_USER" PATH=/usr/local/bin:/usr/bin:/bin "$@"; }
out=$(as_user neodesk --help 2>&1) || fail "neodesk --help as a normal user: $out"
case "$out" in *"neodesk devices list"*) ;; *) fail "neodesk --help as a normal user is not the command line's: $out" ;; esac
code=0
out=$(as_user neodesk --update < /dev/null 2>&1) || code=$?
[ "$code" != 0 ] || fail "neodesk --update as a normal user reported success: $out"
case "$out" in *"sudo neodesk --update"*) ;; *) fail "neodesk --update as a normal user does not say to run it with sudo: $out" ;; esac
# An API token, made as the console makes them, signed in with a password
console=$(console_address) || fail "the console answers neither on https://127.0.0.1:5443 nor on http://127.0.0.1:5000"
make_token "$console" > "$WORK/token" || fail "no API token"
install -o "$TEST_USER" -m 600 "$WORK/token" "/home/$TEST_USER/token"
code=0
out=$(as_user neodesk --url "$console" --insecure --token-file "/home/$TEST_USER/token" devices list 2>&1) || code=$?
[ "$code" = 0 ] || fail "neodesk devices list as a normal user with a token: exit $code: $out"
if grep -qF -f "$WORK/token" <<< "$out"; then fail "neodesk devices list printed the token"; fi
# The same when XDG_CONFIG_HOME still names a folder of another account
# that the user can't read, as sudo and runuser leave it: the address and
# the token are given, so no config file is needed
mkdir -p "$WORK/closed-config" && chmod 700 "$WORK/closed-config"
code=0
out=$(runuser -u "$TEST_USER" -- env XDG_CONFIG_HOME="$WORK/closed-config" HOME="/home/$TEST_USER" PATH=/usr/local/bin:/usr/bin:/bin \
    neodesk --url "$console" --insecure --token-file "/home/$TEST_USER/token" devices list 2>&1) || code=$?
[ "$code" = 0 ] || fail "neodesk devices list as a normal user whose XDG_CONFIG_HOME it can't read: exit $code: $out"
rm -f "$WORK/token" "/home/$TEST_USER/token"

echo "== the command line and the installer's options through neodesk"
out=$(neodesk --version 2>&1) || fail "neodesk --version: $out"
case "$out" in "neodesk "*) ;; *) fail "neodesk --version is not the command line's: $out" ;; esac
out=$(neodesk --update --help 2>&1) || fail "neodesk --update --help: $out"
case "$out" in *"Usage: sudo neodesk-setup"*"--reset-password"*) ;; *) fail "neodesk --update --help is not the installer's help: $out" ;; esac
out=$(neodesk --check-permissions < /dev/null 2>&1) || fail "neodesk --check-permissions failed: $out"
case "$out" in *"management capabilities are ready"*) ;; *) fail "neodesk --check-permissions did not reach the installer: $out" ;; esac
code=0
out=$(neodesk --set-config 2>&1) || code=$?
[ "$code" = 1 ] && [ "$out" = "ERROR: --set-config requires KEY=VALUE" ] || fail "neodesk --set-config: exit $code: $out"
code=0
out=$(neodesk < /dev/null 2>&1) || code=$?
case "$code $out" in "2 "*"neodesk-setup"*) ;; *) fail "a bare neodesk without a terminal: exit $code: $out" ;; esac
out=$(printf '0\n' | NEODESK_CLASSIC_MENU=1 script -qec neodesk /dev/null 2>&1 | tr -d '\r') || true
case "$out" in *"MAIN MENU"*"Goodbye"*) ;; *) fail "a bare sudo neodesk on a terminal did not open the menu: $out" ;; esac

echo "== update by neodesk-setup, from the link of an earlier build"
# neodesk as the link into the server folder that builds before
# 3.6.0-rc.5 placed, and a newer binary: the update copies it to neodesk
ln -sfn "$BIN" "$CLI"
cp "$WORK/neodesk-server" "$WORK/neodesk-server-next"
printf 'the next release\n' >> "$WORK/neodesk-server-next"
inode=$(stat -c %i "$SETUP")
neodesk --update --binary "$WORK/neodesk-server-next" < /dev/null > "$WORK/update.log" 2>&1 \
    || fail "neodesk --update failed: $(tail -n 40 "$WORK/update.log")"
ran_cleanly "neodesk --update" "$WORK/update.log"
grep -qE 'neodesk-server is up to date|Updated to' "$WORK/update.log" || fail "the update did not finish: $(tail -n 20 "$WORK/update.log")"
[ "$(stat -c %i "$SETUP")" != "$inode" ] || fail "neodesk-setup was written over while it ran"
new_layout "after neodesk --update" "$WORK/newest.sh"
cmp -s "$CLI" "$WORK/neodesk-server-next" || fail "the update did not copy the new binary to $CLI"
ls "$BACKUPS"/neodesk_backup_*.tar.gz >/dev/null 2>&1 || fail "the update left no backup in $BACKUPS"
[ ! -e "$OLD_BACKUPS" ] || fail "$OLD_BACKUPS is there after a fresh install and an update"

echo "== an update whose binary does not come up goes back"
# A binary that answers -version and names the commands of a release, but
# does not start a server
{
    printf '#!/bin/sh
'
    printf '# upgrade-env --from FILE; verify-release; neodesk-setup; migrate -mode sqlite2pg; backup [--out DIR]
'
    printf '[ "$1" = -version ] && { echo "neodesk-server 0.0.0 (does not start)"; exit 0; }
exit 1
'
} > "$WORK/broken-server"
chmod 755 "$WORK/broken-server"
code=0
neodesk --update --binary "$WORK/broken-server" < /dev/null > "$WORK/broken-update.log" 2>&1 || code=$?
ran_cleanly "the update to a binary that does not start" "$WORK/broken-update.log"
[ "$code" != 0 ] || fail "an update to a binary that does not start reported success"
grep -q 'putting the previous one back' "$WORK/broken-update.log" || fail "the failed update did not go back: $(tail -n 20 "$WORK/broken-update.log")"
cmp -s "$BIN" "$WORK/neodesk-server-next" || fail "the binary from before is not back"
systemctl is-active --quiet neodesk-server || fail "the server does not run after the way back"
out=$(neodesk --version 2>&1) || fail "neodesk --version after the way back: $out"

echo "== update of an install with the old manager (3.6.0-rc.4)"
rm -f "$SETUP" "$CLI"
install -m 0755 "$WORK/old-manager" "$OLD"
hash -r
[ "$(command -v neodesk)" = "$OLD" ] || fail "the old manager is not the neodesk command: $(command -v neodesk)"
neodesk --update --binary "$WORK/neodesk-server" < /dev/null > "$WORK/old-update.log" 2>&1 \
    || fail "the old manager's update failed: $(tail -n 40 "$WORK/old-update.log")"
ran_cleanly "the old manager's update" "$WORK/old-update.log"
grep -qE 'neodesk-server is up to date|Updated to' "$WORK/old-update.log" || fail "the old manager's update did not finish"
cmp -s "$OLD" "$WORK/newest.sh" || fail "the old manager did not save the newest installer"
old_backup=$(ls -1t "$OLD_BACKUPS"/neodesk_backup_*.tar.gz 2>/dev/null | head -n 1)
[ -n "$old_backup" ] || fail "the old manager made no backup in $OLD_BACKUPS"
# The newest installer at the old place moves itself at its first run and
# hands the call on: the installer's option to neodesk-setup
hash -r
out=$(neodesk --check-permissions < /dev/null 2>&1) || fail "neodesk --check-permissions after the old manager's update: $out"
case "$out" in *"management capabilities are ready"*) ;; *) fail "the first call after the old manager's update: $out" ;; esac
hash -r
new_layout "after the first call" "$WORK/newest.sh"
# The old manager's backups moved, and nothing is left at the old place
[ ! -e "$OLD_BACKUPS" ] && [ ! -L "$OLD_BACKUPS" ] || fail "$OLD_BACKUPS is still there: $(ls -ld "$OLD_BACKUPS" 2>&1)"
[ -f "$BACKUPS/$(basename "$old_backup")" ] || fail "the old manager's backup did not move to $BACKUPS"
tar -tzf "$BACKUPS/$(basename "$old_backup")" >/dev/null || fail "the old manager's backup cannot be read at $BACKUPS"
# The command line's own calls work from then on
out=$(neodesk --version 2>&1) || fail "neodesk --version after the move: $out"
case "$out" in "neodesk "*) ;; *) fail "neodesk --version after the move: $out" ;; esac

echo "== uninstall"
neodesk --uninstall --purge < /dev/null > "$WORK/uninstall.log" 2>&1 \
    || fail "neodesk --uninstall --purge failed: $(tail -n 40 "$WORK/uninstall.log")"
ran_cleanly "the uninstall" "$WORK/uninstall.log"
for f in "$SETUP" "$CLI" "$OLD" "$BIN"; do
    [ ! -e "$f" ] && [ ! -L "$f" ] || fail "the uninstall left $f"
done
userdel -r "$TEST_USER" >/dev/null 2>&1 || true

rm -rf "$WORK"
echo "PASS: the installer's commands on a real install"
